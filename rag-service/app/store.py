"""Qdrant access.

Qdrant holds one collection with two named vectors per chunk — a dense vector
for meaning and a sparse vector for exact tokens. It is a retrieval backend
only: every access decision arrives from the control plane as an explicit list
of knowledge base ids, and is pushed into the query filter so unauthorised
chunks are never read, never scored and never logged.
"""

from __future__ import annotations

import logging
import uuid
from typing import Iterable, Sequence

from qdrant_client import QdrantClient, models

from .config import settings
from .models import Encoded

logger = logging.getLogger(__name__)

DENSE = "dense"
SPARSE = "sparse"
DENSE_SIZE = 1024  # BGE-M3

_client: QdrantClient | None = None


def client() -> QdrantClient:
    global _client
    if _client is None:
        _client = QdrantClient(url=settings.qdrant_url, timeout=60)
        ensure_collection(_client)
    return _client


def ensure_collection(qdrant: QdrantClient) -> None:
    if qdrant.collection_exists(settings.collection):
        return

    logger.info("creating collection %s", settings.collection)
    qdrant.create_collection(
        collection_name=settings.collection,
        vectors_config={DENSE: models.VectorParams(size=DENSE_SIZE, distance=models.Distance.COSINE)},
        sparse_vectors_config={SPARSE: models.SparseVectorParams()},
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


def point_id(document_id: str, chunk_index: int) -> str:
    """A stable id, so re-ingesting a document replaces its chunks."""
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"{document_id}#{chunk_index}"))


def upsert(chunks: Sequence[dict], encoded: Sequence[Encoded]) -> None:
    points = []
    for chunk, vector in zip(chunks, encoded):
        points.append(
            models.PointStruct(
                id=point_id(chunk["document_id"], chunk["chunk_index"]),
                vector={
                    DENSE: vector.dense,
                    SPARSE: models.SparseVector(
                        indices=list(vector.sparse.keys()),
                        values=list(vector.sparse.values()),
                    ),
                },
                payload=chunk,
            )
        )
    client().upsert(collection_name=settings.collection, points=points, wait=True)


def delete_document(document_id: str) -> None:
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
    return client().query_points(
        collection_name=settings.collection,
        query=vector,
        using=DENSE,
        query_filter=_authorized_filter(kb_ids),
        limit=limit,
        with_payload=True,
    ).points


def search_sparse(kb_ids: Sequence[str], sparse: dict[int, float], limit: int) -> list[models.ScoredPoint]:
    if not sparse:
        return []
    return client().query_points(
        collection_name=settings.collection,
        query=models.SparseVector(indices=list(sparse.keys()), values=list(sparse.values())),
        using=SPARSE,
        query_filter=_authorized_filter(kb_ids),
        limit=limit,
        with_payload=True,
    ).points
