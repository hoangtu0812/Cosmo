"""The ingestion pipeline, reported stage by stage.

Parsing and embedding a large manual takes minutes with nothing to show for
it, which is indistinguishable from being stuck. The pipeline is written as a
generator so each stage announces itself as it happens and the caller can
forward that upstream while the work continues.
"""

from __future__ import annotations

import logging
import os
import time
from typing import Iterator

from . import ingest, objects, store, snapshots
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
    target_snapshot_id: str | None = None,
    source_snapshot_id: str | None = None,
    deadline_epoch: float | None = None,
) -> Iterator[dict]:
    """Ingest one document, yielding an event per stage.

    The final event is always either `done` or `error`, so a reader knows the
    stream ended on purpose rather than by a dropped connection.
    """
    started = time.time()
    def check_deadline():
        if deadline_epoch is not None and time.time() >= deadline_epoch:
            raise TimeoutError("ingestion attempt deadline exceeded")

    try:
        check_deadline()
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
        check_deadline()
        if not chunks:
            yield _event("error", "No readable text found in the document")
            return
        yield _event("chunked", f"Split into {len(chunks)} chunks", chunks=len(chunks))

        yield _event("embedding", "Embedding chunks through the workspace model gateway")

        texts = [chunk["text"] for chunk in chunks]
        cached = {}
        if source_snapshot_id and os.environ.get("KNOWLEDGE_REUSE_EMBEDDINGS", "true").lower() == "true":
            try:
                cached = store.reusable_embeddings(collection=snapshots.collection_name(source_snapshot_id),
                    profile=store.profile_collection(gateway), kb_id=kb_id, document_id=document_id, texts=set(texts))
            except Exception:
                logger.warning("source embeddings unavailable; rebuilding document %s", document_id)
        reused = sum(text in cached for text in texts)
        missing = list(dict.fromkeys(text for text in texts if text not in cached))
        for start in range(0, len(missing), EMBED_BATCH):
            check_deadline()
            batch = missing[start : start + EMBED_BATCH]
            vectors = ml.encode(batch, gateway)
            if len(vectors) != len(batch):
                raise ValueError("incomplete embedding batch")
            cached.update(zip(batch, vectors))
            done = sum(text in cached for text in texts)
            yield _event("embedding", f"Prepared {done}/{len(chunks)} chunks", done=done, total=len(chunks))
        encoded = [cached[text] for text in texts]
        yield _event("embedding", f"Reused {reused}/{len(chunks)} unchanged embeddings", done=len(chunks), total=len(chunks))

        yield _event("indexing", "Writing vectors to the index")
        check_deadline()
        collection = store.profile_collection(gateway)
        if target_snapshot_id:
            collection = snapshots.collection_name(target_snapshot_id)
            for chunk in chunks:
                chunk['snapshot_id'] = target_snapshot_id
                chunk['snapshot_profile'] = store.profile_collection(gateway)
        # A durable worker publishes this isolated attempt only after every
        # document succeeds. Legacy callers retain their existing profile path.
        store.upsert(chunks, encoded, collection=collection)
        check_deadline()

        elapsed = time.time() - started
        yield _event(
            "done",
            f"Ready — {len(chunks)} chunks in {elapsed:.1f}s",
            chunks=len(chunks),
            storage_key=key,
            seconds=round(elapsed, 1),
            reused_embeddings=reused,
            embedded_texts=len(missing),
        )
    except Exception as error:  # noqa: BLE001 - the reason belongs in the log the user sees
        logger.exception("ingestion failed for %s", document_id)
        yield _event("error", str(error) or error.__class__.__name__)
