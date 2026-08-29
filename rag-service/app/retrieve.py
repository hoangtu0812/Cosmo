"""Cross-knowledge-base retrieval.

Written out step by step on purpose. Fusion, reranking, diversity and the
final access check are the parts that decide what a model is allowed to say,
so they are ours to read and audit rather than a framework default that can
change underneath us.

Pipeline (§12):

    resolve authorised KBs -> per-KB dense + lexical retrieval -> global RRF
    fusion -> cross-encoder rerank -> deduplication -> diversity ->
    authority/version -> final ACL check
"""

from __future__ import annotations

import logging
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Sequence

from . import lexical
from . import models as ml
from . import store
from .config import settings

logger = logging.getLogger(__name__)

# Reciprocal rank fusion's damping constant. 60 is the value the original
# paper settles on; it keeps a strong hit at rank 1 from swamping a list.
RRF_K = 60


@dataclass
class Candidate:
    point_id: str
    payload: dict
    fused: float = 0.0
    rerank: float = 0.0
    final: float = 0.0
    sources: list[str] = field(default_factory=list)


def _fuse(ranked_lists: Sequence[tuple[str, list]]) -> dict[str, Candidate]:
    """Reciprocal rank fusion over every retrieval list.

    Dense and sparse scores are not comparable — one is a cosine similarity,
    the other a lexical weight sum — so they are combined by rank, never by
    raw score.
    """
    candidates: dict[str, Candidate] = {}
    for source, points in ranked_lists:
        for rank, point in enumerate(points):
            key = str(point.id)
            candidate = candidates.get(key)
            if candidate is None:
                candidate = Candidate(point_id=key, payload=dict(point.payload or {}))
                candidates[key] = candidate
            candidate.fused += 1.0 / (RRF_K + rank + 1)
            candidate.sources.append(source)
    return candidates


def _authority(payload: dict) -> float:
    """A small multiplier for how much a chunk deserves to be believed.

    Similarity alone is not enough (§11): a superseded procedure that reads
    well should still lose to the current one.
    """
    weight = 1.0

    effective = payload.get("effective_date")
    if effective:
        try:
            date = datetime.fromisoformat(str(effective).replace("Z", "+00:00"))
            if date.tzinfo is None:
                date = date.replace(tzinfo=timezone.utc)
            years = (datetime.now(timezone.utc) - date).days / 365.25
            # Halve the bonus every five years, floored so old-but-valid
            # reference material is discounted, not discarded.
            weight *= max(0.85, 1.15 * (0.5 ** (max(years, 0) / 5)))
        except ValueError:
            logger.debug("unparseable effective_date %r", effective)

    version = payload.get("document_version")
    if isinstance(version, int) and version > 1:
        weight *= 1.02

    return weight


def _diversify(candidates: list[Candidate], limit: int, per_document: int) -> list[Candidate]:
    """Keep the ranking from filling up with one document.

    Ten chunks of the same manual answer a question less well than six chunks
    from three sources, and they hide disagreement between sources.
    """
    seen: dict[str, int] = {}
    kept: list[Candidate] = []
    overflow: list[Candidate] = []

    for candidate in candidates:
        document = candidate.payload.get("document_id", "")
        count = seen.get(document, 0)
        if count < per_document:
            seen[document] = count + 1
            kept.append(candidate)
        else:
            overflow.append(candidate)
        if len(kept) == limit:
            return kept

    # Only once every document has had its turn do we top up from the overflow.
    kept.extend(overflow[: limit - len(kept)])
    return kept


def _deduplicate(candidates: list[Candidate]) -> list[Candidate]:
    """Drop chunks whose text has already been kept.

    Overlapping chunks and documents uploaded twice otherwise spend the
    model's context on the same sentences.
    """
    seen: set[str] = set()
    unique: list[Candidate] = []
    for candidate in candidates:
        fingerprint = " ".join(str(candidate.payload.get("text", "")).split())[:400].lower()
        if fingerprint in seen:
            continue
        seen.add(fingerprint)
        unique.append(candidate)
    return unique


def search(query: str, kb_ids: Sequence[str], limit: int | None = None) -> list[dict]:
    """Retrieve the passages that answer a query across authorised KBs.

    `kb_ids` is the effective allow-list resolved by the control plane. An
    empty list means the user has access to nothing, which returns nothing —
    it never means "search everything".
    """
    if not kb_ids or not query.strip():
        return []

    limit = limit or settings.rerank_output
    allowed = set(kb_ids)

    encoded = ml.encode([query])[0]
    # The lexical half runs here rather than at the gateway: it is a
    # tokeniser and a hash, and sending it out of the process would buy
    # nothing but a round trip.
    terms = lexical.query(query)

    # Each KB is searched on its own so no single large KB can crowd the
    # others out before fusion has a chance to weigh them.
    def retrieve(kb_id: str) -> list[tuple[str, list]]:
        found = [(f"dense:{kb_id}", store.search_dense([kb_id], encoded.dense, settings.candidates_per_kb))]
        if terms:
            found.append((f"sparse:{kb_id}", store.search_sparse([kb_id], terms, settings.candidates_per_kb)))
        return found

    ranked_lists: list[tuple[str, list]] = []
    with ThreadPoolExecutor(max_workers=min(8, len(kb_ids))) as pool:
        for result in pool.map(retrieve, kb_ids):
            ranked_lists.extend(result)

    candidates = list(_fuse(ranked_lists).values())
    if not candidates:
        return []

    candidates.sort(key=lambda item: item.fused, reverse=True)
    candidates = _deduplicate(candidates)[: settings.rerank_input]

    scores = ml.rerank(query, [str(item.payload.get("text", "")) for item in candidates])
    for candidate, score in zip(candidates, scores):
        candidate.rerank = score
        candidate.final = score * _authority(candidate.payload)

    candidates.sort(key=lambda item: item.final, reverse=True)
    candidates = _diversify(candidates, limit, settings.max_chunks_per_document)

    # Defence in depth. Retrieval was already filtered in Qdrant; this second
    # check means a mistake in the query filter cannot become a data leak.
    results = []
    for candidate in candidates:
        kb_id = candidate.payload.get("kb_id")
        if kb_id not in allowed:
            logger.error("dropping chunk from unauthorised kb %s", kb_id)
            continue
        results.append(
            {
                "kb_id": kb_id,
                "document_id": candidate.payload.get("document_id"),
                "document_title": candidate.payload.get("document_title"),
                "source": candidate.payload.get("source"),
                "section": candidate.payload.get("section"),
                "page": candidate.payload.get("page"),
                "text": candidate.payload.get("text"),
                "score": round(candidate.final, 6),
                "rerank_score": round(candidate.rerank, 6),
                "fused_score": round(candidate.fused, 6),
                "matched": sorted({source.split(":", 1)[0] for source in candidate.sources}),
            }
        )
    return results
