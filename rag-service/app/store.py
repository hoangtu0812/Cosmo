"""Qdrant access for the dense and lexical vectors of every chunk."""

from __future__ import annotations

import logging
import uuid
from typing import Iterable, Sequence

from qdrant_client import QdrantClient, models

from . import lexical
from .config import settings
from .models import Encoded

logger = logging.getLogger(__name__)

DENSE = "dense"
SPARSE = "sparse"
_client: QdrantClient | None = None


def client() -> QdrantClient:
    global _client
    if _client is None:
        _client = QdrantClient(url=settings.qdrant_url, timeout=60)
    return _client


def has_lexical(qdrant: QdrantClient | None = None) -> bool:
    """Whether this collection carries lexical vectors.

    Qdrant cannot add a sparse vector to a collection created without
    one, so an index built before hybrid retrieval existed stays
    dense-only until it is rebuilt. Reporting that is better than
    writing vectors the index would reject, or searching one that is
    not there.
    """
    qdrant = qdrant or client()
    if not qdrant.collection_exists(settings.collection):
        return True
    sparse = qdrant.get_collection(settings.collection).config.params.sparse_vectors
    return bool(sparse) and SPARSE in sparse


def ensure_collection(qdrant: QdrantClient, vector_size: int) -> None:
    if qdrant.collection_exists(settings.collection):
        collection = qdrant.get_collection(settings.collection)
        vectors = collection.config.params.vectors
        current = vectors.get(DENSE) if isinstance(vectors, dict) else None
        current_size = getattr(current, "size", None)
        if current_size != vector_size:
            raise RuntimeError(
                f"embedding dimension {vector_size} does not match the existing index dimension {current_size}; reindex documents after changing embedding model"
            )
        if not has_lexical(qdrant):
            logger.warning(
                "collection %s predates lexical retrieval and stays dense-only; re-index to enable it",
                settings.collection,
            )
        return

    logger.info("creating collection %s", settings.collection)
    qdrant.create_collection(
        collection_name=settings.collection,
        vectors_config={DENSE: models.VectorParams(size=vector_size, distance=models.Distance.COSINE)},
        # IDF belongs to the index: it is the only place that knows how
        # rare a term is across the corpus, and it stays correct as
        # documents arrive.
        sparse_vectors_config={SPARSE: models.SparseVectorParams(modifier=models.Modifier.IDF)},
    )

    # kb_id carries every access decision, so it is the one field that must be
    # indexed: an unindexed filter field would push Qdrant towards scanning
    # payloads it should never touch.
    for field, schema in (
        ("kb_id", models.PayloadSchemaType.KEYWORD),
        ("document_id", models.PayloadSchemaType.KEYWORD),
        ("status", models.PayloadSchemaType.KEYWORD),
    ):
        qdrant.create_payload_index(
            collection_name=settings.collection,
            field_name=field,
            field_schema=schema,
        )


def reset_collection() -> None:
    """Remove derived vectors while preserving originals in object storage."""
    qdrant = client()
    if qdrant.collection_exists(settings.collection):
        qdrant.delete_collection(collection_name=settings.collection)


def point_id(document_id: str, chunk_index: int) -> str:
    """A stable id, so re-ingesting a document replaces its chunks."""
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"{document_id}#{chunk_index}"))


def upsert(chunks: Sequence[dict], encoded: Sequence[Encoded]) -> None:
    if not encoded:
        return
    ensure_collection(client(), len(encoded[0].dense))
    lexical_supported = has_lexical()
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
    client().upsert(collection_name=settings.collection, points=points, wait=True)


def delete_document(document_id: str) -> None:
    # A full re-index drops the collection before it starts rebuilding. There
    # are no prior chunks to remove on the first document, so this is a normal
    # no-op rather than a failed ingestion.
    if not client().collection_exists(settings.collection):
        return
    client().delete(
        collection_name=settings.collection,
        points_selector=models.FilterSelector(
            filter=models.Filter(
                must=[models.FieldCondition(key="document_id", match=models.MatchValue(value=document_id))]
            )
        ),
        wait=True,
    )


def delete_knowledge_base(kb_id: str) -> None:
    if not client().collection_exists(settings.collection):
        return
    client().delete(
        collection_name=settings.collection,
        points_selector=models.FilterSelector(
            filter=models.Filter(
                must=[models.FieldCondition(key="kb_id", match=models.MatchValue(value=kb_id))]
            )
        ),
        wait=True,
    )


def inspect_document(document_id: str, limit: int = 25) -> dict:
    """Return a bounded view of a document's Qdrant payloads for inspection."""
    points, next_page = client().scroll(
        collection_name=settings.collection,
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


def search_dense(kb_ids: Sequence[str], vector: list[float], limit: int) -> list[models.ScoredPoint]:
    if not client().collection_exists(settings.collection):
        return []
    return client().query_points(
        collection_name=settings.collection,
        query=vector,
        using=DENSE,
        query_filter=_authorized_filter(kb_ids),
        limit=limit,
        with_payload=True,
    ).points


def search_sparse(kb_ids: Sequence[str], sparse: dict[int, float], limit: int) -> list[models.ScoredPoint]:
    if not sparse or not client().collection_exists(settings.collection) or not has_lexical():
        return []
    return client().query_points(
        collection_name=settings.collection,
        query=models.SparseVector(indices=list(sparse.keys()), values=list(sparse.values())),
        using=SPARSE,
        query_filter=_authorized_filter(kb_ids),
        limit=limit,
        with_payload=True,
    ).points
