"""Runtime configuration, read once from the environment."""

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    qdrant_url: str
    collection: str
    profile_reads: bool

    minio_endpoint: str
    minio_access_key: str
    minio_secret_key: str
    minio_bucket: str
    minio_secure: bool

    # Retrieval shape. The doc asks for a wide first pass narrowed by a
    # reranker, not a narrow vector search fed straight to the model.
    candidates_per_kb: int
    rerank_input: int
    rerank_output: int
    max_chunks_per_document: int

    chunk_size: int
    chunk_overlap: int

    # Length normalisation for lexical scoring, counted in terms.
    average_passage_terms: int

    # Azure AI Document Intelligence. Analysis is billed per page, so the mode
    # decides which documents are worth it rather than sending everything.
    layout_endpoint: str
    layout_key: str
    layout_mode: str
    layout_min_chars_per_page: int
    layout_api_version: str
    layout_timeout: float
    layout_poll_interval: float


def load() -> Settings:
    return Settings(
        qdrant_url=os.environ.get("QDRANT_URL", "http://qdrant:6333"),
        collection=os.environ.get("QDRANT_COLLECTION", "cosmo_knowledge"),
        # Temporary rollout switch: legacy reads while profiles are backfilled.
        # New ingestions always write to profiles, never the legacy collection.
        profile_reads=os.environ.get("KNOWLEDGE_PROFILE_READS", "true").lower() == "true",
        minio_endpoint=os.environ.get("MINIO_ENDPOINT", "minio:9000"),
        minio_access_key=os.environ.get("MINIO_ACCESS_KEY", "cosmo"),
        minio_secret_key=os.environ.get("MINIO_SECRET_KEY", "cosmo-secret"),
        minio_bucket=os.environ.get("MINIO_BUCKET", "cosmo-documents"),
        minio_secure=os.environ.get("MINIO_SECURE", "false").lower() == "true",
        candidates_per_kb=int(os.environ.get("CANDIDATES_PER_KB", "60")),
        rerank_input=int(os.environ.get("RERANK_INPUT", "100")),
        rerank_output=int(os.environ.get("RERANK_OUTPUT", "12")),
        max_chunks_per_document=int(os.environ.get("MAX_CHUNKS_PER_DOCUMENT", "3")),
        chunk_size=int(os.environ.get("CHUNK_SIZE", "900")),
        chunk_overlap=int(os.environ.get("CHUNK_OVERLAP", "150")),
        average_passage_terms=int(os.environ.get("AVERAGE_PASSAGE_TERMS", "400")),
        layout_endpoint=os.environ.get("DOCUMENT_LAYOUT_ENDPOINT", "").strip().rstrip("/"),
        layout_key=os.environ.get("DOCUMENT_LAYOUT_KEY", "").strip(),
        layout_mode=os.environ.get("DOCUMENT_LAYOUT_MODE", "auto").strip().lower(),
        layout_min_chars_per_page=int(os.environ.get("DOCUMENT_LAYOUT_MIN_CHARS_PER_PAGE", "120")),
        layout_api_version=os.environ.get("DOCUMENT_LAYOUT_API_VERSION", "2024-11-30").strip(),
        layout_timeout=float(os.environ.get("DOCUMENT_LAYOUT_TIMEOUT", "600")),
        layout_poll_interval=float(os.environ.get("DOCUMENT_LAYOUT_POLL_INTERVAL", "2")),
    )


settings = load()
