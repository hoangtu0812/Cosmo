"""Qdrant access for the dense and lexical vectors of every chunk."""

from __future__ import annotations

import logging
import hashlib
import json
import re
import math
import uuid
from typing import Iterable, Sequence

from qdrant_client import QdrantClient, models

from . import lexical
from .config import settings
from .models import Encoded, GatewaySettings

logger = logging.getLogger(__name__)

DENSE = "dense"
SPARSE = "sparse"
_client: QdrantClient | None = None


def client() -> QdrantClient:
    global _client
    if _client is None:
        _client = QdrantClient(url=settings.qdrant_url, timeout=60)
    return _client


def profile_collection(gateway: GatewaySettings) -> str:
    """The credential owner, endpoint and model define one embedding space.

    Credentials and reranker choices are deliberately excluded: rotation must
    not orphan an index. Qdrant retains/validates the profile's vector dimension.
    Changing a deployment behind the same model ID requires a new model ID.
    """
    identity = json.dumps([gateway.embedding_scope, gateway.base_url.rstrip("/"),
                           gateway.embedding_model], ensure_ascii=False, separators=(",", ":"))
    digest = hashlib.sha256(identity.encode("utf-8")).hexdigest()
    return f"{settings.collection}__p1_{digest}"


class ProfileNotIndexed(RuntimeError):
    pass


def require_profile(collection: str, kb_ids: Sequence[str] = ()) -> None:
    if not client().collection_exists(collection):
        raise ProfileNotIndexed("Embedding profile is not indexed; reindex this Knowledge Base before searching")
    for kb_id in kb_ids:
        if client().count(collection_name=collection, count_filter=_authorized_filter([kb_id]), exact=False).count == 0:
            raise ProfileNotIndexed("Knowledge Base has no indexed documents in this embedding profile; ingest or reindex it first")


def has_lexical(qdrant: QdrantClient | None = None, *, collection: str | None = None) -> bool:
    """Whether this collection carries lexical vectors.

    Qdrant cannot add a sparse vector to a collection created without
    one, so an index built before hybrid retrieval existed stays
    dense-only until it is rebuilt. Reporting that is better than
    writing vectors the index would reject, or searching one that is
    not there.
    """
    qdrant = qdrant or client()
    collection = collection or settings.collection
    if not qdrant.collection_exists(collection):
        return True
    sparse = qdrant.get_collection(collection).config.params.sparse_vectors
    return bool(sparse) and SPARSE in sparse


def ensure_collection(qdrant: QdrantClient, vector_size: int, *, collection: str | None = None) -> None:
    collection = collection or settings.collection
    if qdrant.collection_exists(collection):
        info = qdrant.get_collection(collection)
        vectors = info.config.params.vectors
        current = vectors.get(DENSE) if isinstance(vectors, dict) else None
        current_size = getattr(current, "size", None)
        if current_size != vector_size:
            raise RuntimeError(
                f"embedding dimension {vector_size} does not match the profile dimension {current_size}; configure a new embedding model ID before reindexing"
            )
        if not has_lexical(qdrant, collection=collection):
            logger.warning(
                "collection %s predates lexical retrieval and stays dense-only; re-index to enable it",
                collection,
            )
        return

    logger.info("creating collection %s", collection)
    try:
        qdrant.create_collection(
            collection_name=collection,
            vectors_config={DENSE: models.VectorParams(size=vector_size, distance=models.Distance.COSINE)},
        # IDF belongs to the index: it is the only place that knows how
        # rare a term is across the corpus, and it stays correct as
        # documents arrive.
            sparse_vectors_config={SPARSE: models.SparseVectorParams(modifier=models.Modifier.IDF)},
        )
    except Exception:
        # Another ingestion process may create this same profile concurrently.
        # Re-read and validate its dimension; never drop/recreate the winner.
        if not qdrant.collection_exists(collection):
            raise
        ensure_collection(qdrant, vector_size, collection=collection)
        return

    # kb_id carries every access decision, so it is the one field that must be
    # indexed: an unindexed filter field would push Qdrant towards scanning
    # payloads it should never touch.
    for field, schema in (
        ("kb_id", models.PayloadSchemaType.KEYWORD),
        ("document_id", models.PayloadSchemaType.KEYWORD),
        ("status", models.PayloadSchemaType.KEYWORD),
    ):
        qdrant.create_payload_index(
            collection_name=collection,
            field_name=field,
            field_schema=schema,
        )


def reset_collection() -> None:
    """Remove derived vectors while preserving originals in object storage."""
    qdrant = client()
    collection = settings.collection
    if qdrant.collection_exists(collection):
        qdrant.delete_collection(collection_name=collection)


def point_id(document_id: str, chunk_index: int) -> str:
    """A stable id, so re-ingesting a document replaces its chunks."""
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"{document_id}#{chunk_index}"))


def upsert(chunks: Sequence[dict], encoded: Sequence[Encoded], *, collection: str | None = None) -> None:
    collection = collection or settings.collection
    # Validate the complete replacement before touching the existing document.
    # zip() alone would silently accept missing embeddings and prune good data.
    if not chunks or len(chunks) != len(encoded):
        raise ValueError("a document replacement requires one embedding per chunk")
    document_id = chunks[0]["document_id"]
    kb_id = chunks[0]["kb_id"]
    indices = [chunk["chunk_index"] for chunk in chunks]
    if (not document_id or not kb_id or len(set(indices)) != len(indices)
            or any(chunk["document_id"] != document_id or chunk["kb_id"] != kb_id for chunk in chunks)):
        raise ValueError("a replacement must contain unique chunks from one document and KB")
    dimension = len(encoded[0].dense)
    if not dimension or any(len(vector.dense) != dimension or not all(math.isfinite(v) for v in vector.dense) for vector in encoded):
        raise ValueError("document embeddings must have one dimension and finite values")
    ensure_collection(client(), len(encoded[0].dense), collection=collection)
    lexical_supported = has_lexical(collection=collection)
    points = []
    for chunk, vector in zip(chunks, encoded):
        vectors: dict = {DENSE: vector.dense}
        weights = lexical.encode(str(chunk.get("text", ""))) if lexical_supported else {}
        if weights:
            vectors[SPARSE] = models.SparseVector(
                indices=list(weights.keys()),
                values=list(weights.values()),
            )
        points.append(
            models.PointStruct(
                id=point_id(chunk["document_id"], chunk["chunk_index"]),
                vector=vectors,
                payload=chunk,
            )
        )
    client().upsert(collection_name=collection, points=points, wait=True)
    # Only prune obsolete chunks after the new vectors have been acknowledged.
    # A failed write must never be preceded by deleting the searchable document.
    # Stable IDs make retry after a failed cleanup safe. This is not a snapshot
    # swap: readers may observe old/new chunks during the replacement.
    client().delete(
        collection_name=collection,
        points_selector=models.FilterSelector(filter=models.Filter(
            must=[
                models.FieldCondition(key="document_id", match=models.MatchValue(value=document_id)),
                models.FieldCondition(key="kb_id", match=models.MatchValue(value=kb_id)),
            ],
            must_not=[models.HasIdCondition(has_id=[point.id for point in points])],
        )),
        wait=True,
    )


def managed_collections() -> list[str]:
    """Own only the exact legacy name and our versioned profile namespace."""
    names = [c.name for c in client().get_collections().collections]
    pattern = re.compile(re.escape(settings.collection) + r"__p1_[0-9a-f]{64}$")
    return [name for name in names if name == settings.collection or pattern.fullmatch(name)]


def _delete_across_profiles(field: str, value: str) -> None:
    for collection in managed_collections():
        client().delete(
            collection_name=collection,
            points_selector=models.FilterSelector(filter=models.Filter(must=[
                models.FieldCondition(key=field, match=models.MatchValue(value=value)),
            ])),
            wait=True,
        )


def delete_document(document_id: str) -> None:
    _delete_across_profiles("document_id", document_id)


def delete_knowledge_base(kb_id: str) -> None:
    _delete_across_profiles("kb_id", kb_id)


def inspect_document(document_id: str, limit: int = 25, *, collection: str | None = None) -> dict:
    """Return a bounded view of a document's Qdrant payloads for inspection."""
    collection = collection or settings.collection
    if not client().collection_exists(collection):
        return {"indexed": False, "chunks": [], "total": 0, "truncated": False}
    points, next_page = client().scroll(
        collection_name=collection,
        scroll_filter=models.Filter(
            must=[models.FieldCondition(key="document_id", match=models.MatchValue(value=document_id))]
        ),
        limit=limit,
        with_payload=True,
        with_vectors=False,
    )
    chunks = []
    for point in points:
        payload = point.payload or {}
        chunks.append(
            {
                "chunk_index": payload.get("chunk_index", 0),
                "section": payload.get("section") or "",
                "page": str(payload.get("page") or ""),
                "text": payload.get("text") or "",
            }
        )
    chunks.sort(key=lambda item: item["chunk_index"])
    return {"indexed": bool(chunks), "chunks": chunks, "total": len(chunks), "truncated": next_page is not None}


def _authorized_filter(kb_ids: Iterable[str]) -> models.Filter:
    """Build the pre-retrieval ACL filter.

    This is the single point where authorisation reaches the vector store. It
    is an allow-list of knowledge base ids resolved upstream — never a
    post-filter over a wider search.
    """
    return models.Filter(
        must=[
            models.FieldCondition(key="kb_id", match=models.MatchAny(any=list(kb_ids))),
            models.FieldCondition(key="status", match=models.MatchValue(value="active")),
        ]
    )


def search_dense(
    kb_ids: Sequence[str],
    vector: list[float],
    limit: int,
    score_threshold: float | None = None,
    *, collection: str | None = None,
) -> list[models.ScoredPoint]:
    collection = collection or settings.collection
    if not client().collection_exists(collection):
        return []
    return client().query_points(
        collection_name=collection,
        query=vector,
        using=DENSE,
        query_filter=_authorized_filter(kb_ids),
        limit=limit,
        score_threshold=score_threshold,
        with_payload=True,
    ).points


def search_sparse(kb_ids: Sequence[str], sparse: dict[int, float], limit: int, *, collection: str | None = None) -> list[models.ScoredPoint]:
    collection = collection or settings.collection
    if not sparse or not client().collection_exists(collection) or not has_lexical(collection=collection):
        return []
    return client().query_points(
        collection_name=collection,
        query=models.SparseVector(indices=list(sparse.keys()), values=list(sparse.values())),
        using=SPARSE,
        query_filter=_authorized_filter(kb_ids),
        limit=limit,
        with_payload=True,
    ).points
