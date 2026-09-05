"""The ingestion pipeline, reported stage by stage.

Parsing and embedding a large manual takes minutes with nothing to show for
it, which is indistinguishable from being stuck. The pipeline is written as a
generator so each stage announces itself as it happens and the caller can
forward that upstream while the work continues.
"""

from __future__ import annotations

import logging
import time
from typing import Iterator

from . import ingest, objects, store
from . import models as ml

logger = logging.getLogger(__name__)

# Texts are embedded in batches so progress is reported while a long document
# is still being processed, rather than once at the end.
EMBED_BATCH = 16


def _event(stage: str, message: str, **fields) -> dict:
    return {"stage": stage, "message": message, "at": time.time(), **fields}


def _reported(stages: Iterator[dict]) -> Iterator[dict]:
    """Forward the parser's own stages, stamped like every other event.

    The parser announces a slow route before taking it, so its stages have to
    reach the reader as they happen rather than being collected at the end.
    """
    while True:
        try:
            stage = next(stages)
        except StopIteration as finished:
            return finished.value or []
        yield _event(stage["stage"], stage["message"])


def run(
    *,
    content: bytes,
    filename: str,
    content_type: str,
    kb_id: str,
    document_id: str,
    title: str,
    document_version: int,
    effective_date: str | None,
    gateway: ml.GatewaySettings,
    layout_mode: str | None = None,
    storage_key: str | None = None,
    chunk_size: int | None = None,
    chunk_overlap: int | None = None,
) -> Iterator[dict]:
    """Ingest one document, yielding an event per stage.

    The final event is always either `done` or `error`, so a reader knows the
    stream ended on purpose rather than by a dropped connection.
    """
    started = time.time()

    try:
        yield _event("received", f"Received {filename} ({len(content):,} bytes)")

        if storage_key:
            # Re-indexing reads an original that is already in object storage.
            # Writing it back would be a copy of itself, and a failed write
            # would lose the only copy the index can be rebuilt from.
            key = storage_key
            yield _event("stored", f"Original already stored as {key}", storage_key=key)
        else:
            key = ingest.storage_key(kb_id, document_id, filename)
            objects.put(key, content, content_type)
            yield _event("stored", f"Original stored as {key}", storage_key=key)

        yield _event("parsing", "Parsing document")
        chunks = yield from _reported(ingest.parse(
            content=content,
            filename=filename,
            kb_id=kb_id,
            document_id=document_id,
            document_version=document_version,
            title=title,
            effective_date=effective_date,
            layout_mode=layout_mode,
            chunk_size=chunk_size,
            chunk_overlap=chunk_overlap,
        ))
        if not chunks:
            yield _event("error", "No readable text found in the document")
            return
        yield _event("chunked", f"Split into {len(chunks)} chunks", chunks=len(chunks))

        yield _event("embedding", "Embedding chunks through the workspace model gateway")

        encoded = []
        for start in range(0, len(chunks), EMBED_BATCH):
            batch = chunks[start : start + EMBED_BATCH]
            encoded.extend(ml.encode([chunk["text"] for chunk in batch], gateway))
            done = len(encoded)
            yield _event(
                "embedding",
                f"Embedded {done}/{len(chunks)} chunks",
                done=done,
                total=len(chunks),
            )

        yield _event("indexing", "Writing vectors to the index")
        # The store writes the replacement first, then prunes obsolete chunks.
        store.upsert(chunks, encoded, collection=store.profile_collection(gateway))

        elapsed = time.time() - started
        yield _event(
            "done",
            f"Ready — {len(chunks)} chunks in {elapsed:.1f}s",
            chunks=len(chunks),
            storage_key=key,
            seconds=round(elapsed, 1),
        )
    except Exception as error:  # noqa: BLE001 - the reason belongs in the log the user sees
        logger.exception("ingestion failed for %s", document_id)
        yield _event("error", str(error) or error.__class__.__name__)
