"""MinIO object storage for original documents.

Postgres keeps only the reference; the bytes live here (§15).
"""

from __future__ import annotations

import io
import logging

from minio import Minio

from .config import settings

logger = logging.getLogger(__name__)

_client: Minio | None = None


def client() -> Minio:
    global _client
    if _client is None:
        _client = Minio(
            settings.minio_endpoint,
            access_key=settings.minio_access_key,
            secret_key=settings.minio_secret_key,
            secure=settings.minio_secure,
        )
        if not _client.bucket_exists(settings.minio_bucket):
            logger.info("creating bucket %s", settings.minio_bucket)
            _client.make_bucket(settings.minio_bucket)
    return _client


def put(key: str, content: bytes, content_type: str) -> None:
    client().put_object(
        settings.minio_bucket,
        key,
        io.BytesIO(content),
        length=len(content),
        content_type=content_type or "application/octet-stream",
    )


def get(key: str) -> bytes:
    response = client().get_object(settings.minio_bucket, key)
    try:
        return response.read()
    finally:
        response.close()
        response.release_conn()


def delete(key: str) -> None:
    client().remove_object(settings.minio_bucket, key)
