"""The Knowledge Plane's data service.

This service parses, embeds, stores and retrieves. It holds no notion of users,
workspaces or roles: every request arrives with the access decision already
made, expressed as an explicit list of knowledge base ids. Refusing to know
about identity here is what keeps authorisation in one place.
"""

from __future__ import annotations

import base64
import json
import logging
import os

from fastapi import Body, FastAPI, Header, HTTPException
from fastapi.responses import Response, StreamingResponse
from pydantic import BaseModel, Field

from . import ingest, objects, pipeline, retrieve, store
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
    # Exactly one source: the bytes inline for a new upload, or the key of an
    # original already in object storage for a re-index.
    content_base64: str = ""
    storage_key: str | None = Field(default=None, max_length=512)
    title: str = ""
    document_version: int = 1
    effective_date: str | None = None
    # How hard to work at reading a PDF, chosen by the owning knowledge base.
    layout_mode: str | None = Field(default=None, max_length=20)
    embedding_model: str | None = Field(default=None, max_length=200)
    reranker_model: str | None = Field(default=None, max_length=200)
    chunk_size: int | None = Field(default=None, ge=256, le=4096)
    chunk_overlap: int | None = Field(default=None, ge=0, le=2048)


class SearchRequest(BaseModel):
    query: str
    kb_ids: list[str] = Field(default_factory=list)
    limit: int | None = None
    embedding_model: str | None = Field(default=None, max_length=200)
    reranker_model: str | None = Field(default=None, max_length=200)
    retrieval_mode: str = Field(default="hybrid", pattern="^(semantic|keyword|hybrid)$")
    rerank_enabled: bool = True
    score_threshold: float = Field(default=0.2, ge=0, le=1)


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


@app.post("/ingest")
def ingest_document(
    request: IngestRequest = Body(...),
    gateway_base_url: str | None = Header(default=None, alias="X-Cosmo-Gateway-Base-URL"),
    gateway_api_key: str | None = Header(default=None, alias="X-Cosmo-Gateway-API-Key"),
):
    """Ingest a document, streaming one JSON event per line as it progresses.

    NDJSON rather than a single response: the caller needs to show what is
    happening during the minutes this takes, and a stream lets it forward each
    stage without holding the whole pipeline in memory.
    """
    gateway = ml.gateway_settings(request.embedding_model, request.reranker_model, gateway_base_url, gateway_api_key)

    if request.storage_key:
        # A re-index names the original rather than resending it: the control
        # plane would otherwise read every document out of object storage only
        # to base64-encode it back to the service that stored it.
        try:
            content = objects.get(request.storage_key)
        except Exception as error:  # noqa: BLE001 - the key is the useful part
            raise HTTPException(status_code=404, detail=f"could not read {request.storage_key}: {error}") from error
    else:
        try:
            content = base64.b64decode(request.content_base64)
        except Exception as error:  # noqa: BLE001
            raise HTTPException(status_code=400, detail="content_base64 is not valid base64") from error

    if not content:
        raise HTTPException(status_code=400, detail="document is empty")

    def stream():
        for event in pipeline.run(
            content=content,
            filename=request.filename,
            content_type=request.content_type,
            kb_id=request.kb_id,
            document_id=request.document_id,
            title=request.title or request.filename,
            document_version=request.document_version,
            effective_date=request.effective_date,
            layout_mode=request.layout_mode,
            storage_key=request.storage_key,
            gateway=gateway,
            chunk_size=request.chunk_size,
            chunk_overlap=request.chunk_overlap,
        ):
            yield json.dumps(event, ensure_ascii=False) + "\n"

    return StreamingResponse(stream(), media_type="application/x-ndjson")


@app.post("/search", response_model=SearchResponse)
def search(
    request: SearchRequest = Body(...),
    gateway_base_url: str | None = Header(default=None, alias="X-Cosmo-Gateway-Base-URL"),
    gateway_api_key: str | None = Header(default=None, alias="X-Cosmo-Gateway-API-Key"),
) -> SearchResponse:
    gateway = ml.gateway_settings(request.embedding_model, request.reranker_model, gateway_base_url, gateway_api_key)
    return SearchResponse(results=retrieve.search(
        request.query,
        request.kb_ids,
        request.limit,
        gateway=gateway,
        retrieval_mode=request.retrieval_mode,
        rerank_enabled=request.rerank_enabled,
        score_threshold=request.score_threshold,
    ))


@app.post("/collections/reset")
def reset_collection() -> dict:
    """Drop the derived vector collection before a control-plane re-index."""
    store.reset_collection()
    return {"reset": True}


@app.delete("/documents/{document_id}")
def delete_document(document_id: str, storage_key: str | None = None) -> dict:
    store.delete_document(document_id)
    if storage_key:
        try:
            objects.delete(storage_key)
        except Exception:  # noqa: BLE001 - the chunks are gone either way
            logger.warning("could not remove object %s", storage_key)
    return {"deleted": document_id}


@app.get("/documents/{document_id}/inspection")
def inspect_document(document_id: str) -> dict:
    return store.inspect_document(document_id)


@app.get("/documents/{document_id}/original")
def open_original_document(document_id: str, storage_key: str | None = None) -> Response:
    if not storage_key:
        raise HTTPException(status_code=400, detail="storage_key is required")
    try:
        return Response(content=objects.get(storage_key), media_type="application/octet-stream")
    except Exception as error:  # noqa: BLE001 - preserve a useful upstream failure
        raise HTTPException(status_code=404, detail=str(error)) from error


@app.delete("/knowledge-bases/{kb_id}")
def delete_knowledge_base(kb_id: str) -> dict:
    store.delete_knowledge_base(kb_id)
    return {"deleted": kb_id}
