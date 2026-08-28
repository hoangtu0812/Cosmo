"""Runtime configuration, read once from the environment.

Model identifiers are configurable so an air-gapped deployment can point them
at a local mirror without touching code.
"""

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    qdrant_url: str
    collection: str

    minio_endpoint: str
    minio_access_key: str
    minio_secret_key: str
    minio_bucket: str
    minio_secure: bool

    embedding_model: str
    reranker_model: str
    model_cache: str

    # Retrieval shape. The doc asks for a wide first pass narrowed by a
    # reranker, not a narrow vector search fed straight to the model.
    candidates_per_kb: int
    rerank_input: int
    rerank_output: int
    max_chunks_per_document: int

    chunk_size: int
    chunk_overlap: int


def load() -> Settings:
    return Settings(
        qdrant_url=os.environ.get("QDRANT_URL", "http://qdrant:6333"),
        collection=os.environ.get("QDRANT_COLLECTION", "cosmo_knowledge"),
        minio_endpoint=os.environ.get("MINIO_ENDPOINT", "minio:9000"),
        minio_access_key=os.environ.get("MINIO_ACCESS_KEY", "cosmo"),
        minio_secret_key=os.environ.get("MINIO_SECRET_KEY", "cosmo-secret"),
        minio_bucket=os.environ.get("MINIO_BUCKET", "cosmo-documents"),
        minio_secure=os.environ.get("MINIO_SECURE", "false").lower() == "true",
        embedding_model=os.environ.get("EMBEDDING_MODEL", "BAAI/bge-m3"),
        reranker_model=os.environ.get("RERANKER_MODEL", "BAAI/bge-reranker-v2-m3"),
        model_cache=os.environ.get("MODEL_CACHE", "/models"),
        candidates_per_kb=int(os.environ.get("CANDIDATES_PER_KB", "60")),
        rerank_input=int(os.environ.get("RERANK_INPUT", "100")),
        rerank_output=int(os.environ.get("RERANK_OUTPUT", "12")),
        max_chunks_per_document=int(os.environ.get("MAX_CHUNKS_PER_DOCUMENT", "3")),
        chunk_size=int(os.environ.get("CHUNK_SIZE", "900")),
        chunk_overlap=int(os.environ.get("CHUNK_OVERLAP", "150")),
    )


settings = load()
