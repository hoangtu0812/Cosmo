"""The Knowledge Plane's data service.

This service parses, embeds, stores and retrieves. It holds no notion of users,
workspaces or roles: every request arrives with the access decision already
made, expressed as an explicit list of knowledge base ids. Refusing to know
about identity here is what keeps authorisation in one place.
"""

from __future__ import annotations

from dataclasses import replace
import base64
import json
import logging
import os

from fastapi import Body, FastAPI, Header, HTTPException
from fastapi.responses import JSONResponse, Response, StreamingResponse
from pydantic import BaseModel, Field

from . import ingest, objects, pipeline, retrieve, store, layout
from . import snapshots
from . import models as ml
from .config import settings

logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"))
logger = logging.getLogger(__name__)

app = FastAPI(title="Cosmo Knowledge Service", docs_url=None, redoc_url=None)


@app.get("/capabilities")
def capabilities() -> dict:
    return {"layout_available": layout.configured()}


@app.exception_handler(store.ProfileNotIndexed)
async def profile_not_indexed(_request, error):
    return JSONResponse(status_code=503, content={"detail": str(error), "code": "embedding_profile_not_indexed"})


class IngestRequest(BaseModel):
    checkpoint_snapshot_id: str | None = Field(default=None, pattern=r"^kbs_[0-9a-f]{32}$")
    checkpoint_chunks: int = Field(default=0, ge=0, le=100000)
    source_snapshot_id: str | None = Field(default=None, pattern=r"^kbs_[0-9a-f]{32}$")
    target_snapshot_id: str | None = Field(default=None, pattern=r"^kbs_[0-9a-f]{32}$")
    deadline_epoch: float | None = Field(default=None, allow_inf_nan=False)
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


class ExtractRequest(BaseModel):
    """One file, read for its text and nothing else.

    No kb_id, no document id, no embedding model: a file attached to a message
    is read once and answered about. Chunking, embedding and storing it would
    be building a collection nobody asked for.
    """

    filename: str
    content_type: str = "application/octet-stream"
    content_base64: str
    layout_mode: str | None = None


class ExtractResponse(BaseModel):
    text: str
    chars: int
    is_truncated: bool


class SearchRequest(BaseModel):
    query: str
    snapshot_id: str | None = Field(default=None, pattern=r"^kbs_[0-9a-f]{32}$")
    kb_ids: list[str] = Field(default_factory=list)
    limit: int | None = None
    embedding_model: str | None = Field(default=None, max_length=200)
    reranker_model: str | None = Field(default=None, max_length=200)
    retrieval_mode: str = Field(default="hybrid", pattern="^(semantic|keyword|hybrid)$")
    rerank_enabled: bool = True
    score_threshold: float = Field(default=0.2, ge=0, le=1)


class SearchResponse(BaseModel):
    results: list[dict]
    observations: list[dict] = Field(default_factory=list)


@app.get("/health")
def health() -> dict:
    """Liveness only.

    Deliberately does not touch the models: they load on first use and take
    minutes to download, and a health probe that waits for them would keep the
    service out of rotation long after it is able to serve.
    """
    return {"status": "ok", "collection": settings.collection, "profile_reads": settings.profile_reads}


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
    embedding_scope: str | None = Header(default=None, alias="X-Cosmo-Embedding-Scope"),
):
    """Ingest a document, streaming one JSON event per line as it progresses.

    NDJSON rather than a single response: the caller needs to show what is
    happening during the minutes this takes, and a stream lets it forward each
    stage without holding the whole pipeline in memory.
    """
    gateway = ml.gateway_settings(request.embedding_model, request.reranker_model, gateway_base_url, gateway_api_key, embedding_scope)

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
        observations = []
        observed_gateway = replace(gateway, observer=observations.append)
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
            gateway=observed_gateway,
            chunk_size=request.chunk_size,
            chunk_overlap=request.chunk_overlap,
            target_snapshot_id=request.target_snapshot_id,
            source_snapshot_id=request.source_snapshot_id,
            checkpoint_snapshot_id=request.checkpoint_snapshot_id,
            checkpoint_chunks=request.checkpoint_chunks,
            deadline_epoch=request.deadline_epoch,
        ):
            for observation in observations:
                observation["phase"] = "rag:ingest:" + observation["phase"]
                yield json.dumps({"stage": "accounting", "observation": observation}) + "\n"
            observations.clear()
            yield json.dumps(event, ensure_ascii=False) + "\n"

    return StreamingResponse(stream(), media_type="application/x-ndjson")


class OriginalUpload(BaseModel):
    document_id: str = Field(pattern=r"^doc_[A-Za-z0-9_-]{8,80}$")
    content_base64: str = Field(max_length=89478488)
    content_type: str = Field(default="application/octet-stream", max_length=256)


@app.post("/originals")
def store_original(request: OriginalUpload) -> dict:
    try:
        content = base64.b64decode(request.content_base64, validate=True)
    except ValueError as error:
        raise HTTPException(400, "invalid original encoding") from error
    if not content or len(content) > 64 * 1024 * 1024:
        raise HTTPException(400, "invalid original size")
    key = "knowledge-uploads/" + request.document_id
    objects.put(key, content, request.content_type)
    return {"storage_key": key, "size_bytes": len(content)}


@app.post("/search", response_model=SearchResponse)
def search(
    request: SearchRequest = Body(...),
    gateway_base_url: str | None = Header(default=None, alias="X-Cosmo-Gateway-Base-URL"),
    gateway_api_key: str | None = Header(default=None, alias="X-Cosmo-Gateway-API-Key"),
    embedding_scope: str | None = Header(default=None, alias="X-Cosmo-Embedding-Scope"),
) -> SearchResponse:
    gateway = ml.gateway_settings(request.embedding_model, request.reranker_model, gateway_base_url, gateway_api_key, embedding_scope)
    observations = []
    gateway = replace(gateway, observer=observations.append)
    try:
        results = retrieve.search(request.query, request.kb_ids, request.limit, gateway=gateway,
            retrieval_mode=request.retrieval_mode, rerank_enabled=request.rerank_enabled,
            score_threshold=request.score_threshold, snapshot_id=request.snapshot_id)
    except Exception as error:
        for observation in observations:
            observation["phase"] = "rag:search:" + observation["phase"]
        code = "embedding_profile_not_indexed" if isinstance(error, store.ProfileNotIndexed) else "retrieval_failed"
        return JSONResponse(status_code=503, content={"detail": "Retrieval failed", "code": code, "observations": observations})
    for observation in observations:
        observation["phase"] = "rag:search:" + observation["phase"]
    return SearchResponse(results=results, observations=observations)



class SnapshotRequest(BaseModel):
    source_snapshot_id: str | None = Field(default=None, pattern=r"^kbs_[0-9a-f]{32}$")
    snapshot_id: str = Field(pattern=r"^kbs_[0-9a-f]{32}$")
    kb_id: str
    embedding_model: str
    documents: dict[str, int]
    originals: dict[str, dict] | None = None
    deadline_epoch: float | None = None


@app.post("/snapshots")
def create_snapshot(request: SnapshotRequest,
                    gateway_base_url: str | None = Header(default=None, alias="X-Cosmo-Gateway-Base-URL"),
                    embedding_scope: str | None = Header(default=None, alias="X-Cosmo-Embedding-Scope")) -> dict:
    gateway = ml.gateway_settings(request.embedding_model, None, gateway_base_url, None, embedding_scope)
    return snapshots.create(request.snapshot_id, request.kb_id, gateway, request.documents, request.originals, request.deadline_epoch, request.source_snapshot_id)


@app.delete("/snapshots/{snapshot_id}")
def discard_snapshot(snapshot_id: str) -> dict:
    snapshots.discard(snapshot_id)
    return {"deleted": snapshot_id}


@app.post("/extract", response_model=ExtractResponse)
def extract(request: ExtractRequest = Body(...)) -> ExtractResponse:
    """Read one file into text, for a message that attached it.

    Not a small /ingest: nothing is stored, chunked or embedded. The reading is
    the same though - the same readers, the same layout analysis for a scan -
    so a document attached to a question reads the way it would as knowledge.
    """
    try:
        content = base64.b64decode(request.content_base64)
    except Exception as error:  # noqa: BLE001
        raise HTTPException(status_code=400, detail="content_base64 is not valid base64") from error
    if not content:
        raise HTTPException(status_code=400, detail="document is empty")

    try:
        text = ingest.read_text(content=content, filename=request.filename, layout_mode=request.layout_mode)
    except RuntimeError as error:
        # A format with no reader: the caller can say so rather than attaching
        # mojibake to the conversation.
        raise HTTPException(status_code=415, detail=str(error)) from error
    if not text.strip():
        raise HTTPException(status_code=422, detail="no readable text found in the document")

    limit = 200_000
    return ExtractResponse(text=text[:limit], chars=len(text), is_truncated=len(text) > limit)


@app.post("/collections/reset")
def reset_collection() -> dict:
    """A rebuild must retain existing profiles, including the legacy index."""
    raise HTTPException(status_code=410, detail="Global index reset is retired; reindex documents into embedding profiles")


@app.delete("/documents/{document_id}")
def delete_document(document_id: str, storage_key: str | None = None, snapshot_id: str | None = None) -> dict:
    if snapshot_id:
        collection = snapshots.collection_name(snapshot_id)
        store.client().delete(collection_name=collection, points_selector=store.models.FilterSelector(
            filter=store.models.Filter(must=[store.models.FieldCondition(key="document_id", match=store.models.MatchValue(value=document_id))])), wait=True)
    store.delete_document(document_id)
    if storage_key:
        try:
            objects.delete(storage_key)
        except Exception:  # noqa: BLE001 - the chunks are gone either way
            logger.warning("could not remove object %s", storage_key)
    return {"deleted": document_id}


@app.get("/documents/{document_id}/inspection")
def inspect_document(
    document_id: str,
    embedding_model: str,
    snapshot_id: str | None = None,
    kb_id: str | None = None,
    gateway_base_url: str | None = Header(default=None, alias="X-Cosmo-Gateway-Base-URL"),
    embedding_scope: str | None = Header(default=None, alias="X-Cosmo-Embedding-Scope"),
) -> dict:
    gateway = ml.gateway_settings(embedding_model, None, gateway_base_url, None, embedding_scope)
    collection = store.profile_collection(gateway) if settings.profile_reads else settings.collection
    if snapshot_id:
        collection = snapshots.resolve(snapshot_id, [kb_id], gateway)
    return store.inspect_document(document_id, collection=collection)


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
    snapshots.delete_knowledge_base(kb_id)
    return {"deleted": kb_id}
