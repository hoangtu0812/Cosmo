"""The Knowledge Plane's data service.

This service parses, embeds, stores and retrieves. It holds no notion of users,
workspaces or roles: every request arrives with the access decision already
made, expressed as an explicit list of knowledge base ids. Refusing to know
about identity here is what keeps authorisation in one place.
"""

from __future__ import annotations

import base64
import logging
import os

from fastapi import Body, FastAPI, HTTPException
from pydantic import BaseModel, Field

from . import ingest, objects, retrieve, store
from . import models as ml
from .config import settings

logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"))
logger = logging.getLogger(__name__)

app = FastAPI(title="Cosmo Knowledge Service", docs_url=None, redoc_url=None)


class IngestRequest(BaseModel):
    kb_id: str
    document_id: str
    filename: str
    content_type: str = "application/octet-stream"
    content_base64: str
    title: str = ""
    document_version: int = 1
    effective_date: str | None = None


class IngestResponse(BaseModel):
    document_id: str
    chunks: int
    storage_key: str


class SearchRequest(BaseModel):
    query: str
    kb_ids: list[str] = Field(default_factory=list)
    limit: int | None = None


class SearchResponse(BaseModel):
    results: list[dict]


@app.get("/health")
def health() -> dict:
    """Liveness only.

    Deliberately does not touch the models: they load on first use and take
    minutes to download, and a health probe that waits for them would keep the
    service out of rotation long after it is able to serve.
    """
    return {"status": "ok", "collection": settings.collection}


@app.get("/ready")
def ready() -> dict:
    """Readiness — the backing stores must actually answer."""
    try:
        store.client().get_collections()
        objects.client().bucket_exists(settings.minio_bucket)
    except Exception as error:  # noqa: BLE001 - report the cause to the probe
        raise HTTPException(status_code=503, detail=str(error)) from error
    return {"status": "ready"}


@app.post("/ingest", response_model=IngestResponse)
def ingest_document(request: IngestRequest = Body(...)) -> IngestResponse:
    try:
        content = base64.b64decode(request.content_base64)
    except Exception as error:  # noqa: BLE001
        raise HTTPException(status_code=400, detail="content_base64 is not valid base64") from error

    if not content:
        raise HTTPException(status_code=400, detail="document is empty")

    key = ingest.storage_key(request.kb_id, request.document_id, request.filename)
    objects.put(key, content, request.content_type)

    chunks = ingest.chunk(
        content=content,
        filename=request.filename,
        kb_id=request.kb_id,
        document_id=request.document_id,
        document_version=request.document_version,
        title=request.title or request.filename,
        effective_date=request.effective_date,
    )
    if not chunks:
        raise HTTPException(status_code=422, detail="no readable text in document")

    # Replace rather than append, so re-ingesting a document cannot leave
    # chunks of the previous version behind to be retrieved later.
    store.delete_document(request.document_id)
    encoded = ml.encode([chunk["text"] for chunk in chunks])
    store.upsert(chunks, encoded)

    logger.info("ingested %s (%d chunks) into %s", request.filename, len(chunks), request.kb_id)
    return IngestResponse(document_id=request.document_id, chunks=len(chunks), storage_key=key)


@app.post("/search", response_model=SearchResponse)
def search(request: SearchRequest = Body(...)) -> SearchResponse:
    return SearchResponse(results=retrieve.search(request.query, request.kb_ids, request.limit))


@app.delete("/documents/{document_id}")
def delete_document(document_id: str, storage_key: str | None = None) -> dict:
    store.delete_document(document_id)
    if storage_key:
        try:
            objects.delete(storage_key)
        except Exception:  # noqa: BLE001 - the chunks are gone either way
            logger.warning("could not remove object %s", storage_key)
    return {"deleted": document_id}


@app.delete("/knowledge-bases/{kb_id}")
def delete_knowledge_base(kb_id: str) -> dict:
    store.delete_knowledge_base(kb_id)
    return {"deleted": kb_id}
