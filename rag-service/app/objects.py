"""MinIO object storage for original documents.

Postgres keeps only the reference; the bytes live here (§15).
"""

from __future__ import annotations

import io
import logging

from minio import Minio
from minio.commonconfig import CopySource

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


def copy_original(source: str, target: str) -> dict:
    storage = client()
    original = storage.stat_object(settings.minio_bucket, source)
    storage.copy_object(settings.minio_bucket, target,
                        CopySource(settings.minio_bucket, source, match_etag=original.etag))
    copied = storage.stat_object(settings.minio_bucket, target)
    if copied.size != original.size:
        raise RuntimeError("snapshot original size mismatch")
    return {"size_bytes": copied.size, "etag": copied.etag}


def delete_prefix(prefix: str) -> None:
    for item in client().list_objects(settings.minio_bucket, prefix=prefix, recursive=True):
        client().remove_object(settings.minio_bucket, item.object_name)
