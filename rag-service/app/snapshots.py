"""Immutable copies of indexed evidence, separate from mutable profiles."""
import hashlib
import json
import re
import time
from urllib.parse import quote

from . import store, objects
from .models import GatewaySettings


def collection_name(snapshot_id: str) -> str:
    if not re.fullmatch(r"kbs_[0-9a-f]{32}", snapshot_id):
        raise ValueError("invalid knowledge snapshot ID")
    return store.settings.collection + "__s1_" + snapshot_id[4:]


def create(snapshot_id: str, kb_id: str, gateway: GatewaySettings, documents: dict[str, int], originals: dict | None = None, deadline_epoch: float | None = None) -> dict:
    deadline = min(deadline_epoch if deadline_epoch is not None else time.time() + 300, time.time() + 300)
    def check_deadline():
        if time.time() >= deadline:
            raise TimeoutError("snapshot copy deadline exceeded")
    check_deadline()
    if not documents or len(documents) > 10000 or any(n <= 0 for n in documents.values()):
        raise ValueError("snapshot requires a bounded manifest of indexed documents")
    source = store.profile_collection(gateway)
    store.require_profile(source, [kb_id])
    target = collection_name(snapshot_id)
    qdrant = store.client()
    # Never append to or overwrite a prior snapshot, including an uncertain retry.
    info = qdrant.get_collection(source)
    qdrant.create_collection(collection_name=target, vectors_config=info.config.params.vectors,
                             sparse_vectors_config=info.config.params.sparse_vectors)
    try:
        copied_originals = {}
        if originals is not None:
            if set(originals) != set(documents):
                raise ValueError("snapshot originals do not match document manifest")
            for doc, original in originals.items():
                check_deadline()
                target_key = f"knowledge-snapshots/{snapshot_id}/{quote(doc, safe='')}"
                stored = objects.copy_original(original["storage_key"], target_key)
                if stored["size_bytes"] != original["size_bytes"]:
                    raise ValueError("snapshot original differs from document manifest")
                copied_originals[doc] = {**original, **stored, "storage_key": target_key}
        counts = {doc: 0 for doc in documents}
        digest = hashlib.sha256()
        offset = None
        while True:
            check_deadline()
            points, offset = qdrant.scroll(collection_name=source, scroll_filter=store._authorized_filter([kb_id]),
                                           offset=offset, limit=128, with_payload=True, with_vectors=True)
            copied = []
            for point in points:
                payload = dict(point.payload or {})
                doc = payload.get("document_id")
                if doc not in counts:
                    raise ValueError("snapshot corpus does not match document manifest")
                counts[doc] += 1
                if counts[doc] > documents[doc]:
                    raise ValueError("snapshot chunk count exceeds document manifest")
                payload["snapshot_id"] = snapshot_id
                # Keep the profile binding inside every immutable payload.
                payload["snapshot_profile"] = source
                digest.update(json.dumps([str(point.id), payload], sort_keys=True, ensure_ascii=False).encode())
                copied.append(store.models.PointStruct(id=point.id, payload=payload, vector=point.vector))
            if copied:
                check_deadline()
                qdrant.upsert(collection_name=target, points=copied, wait=True)
            if offset is None:
                break
        if counts != documents:
            raise ValueError("snapshot corpus is incomplete")
        for field in ("kb_id", "document_id", "status"):
            check_deadline()
            qdrant.create_payload_index(collection_name=target, field_name=field, field_schema=store.models.PayloadSchemaType.KEYWORD)
        check_deadline()
        return {"snapshot_id": snapshot_id, "chunks": sum(counts.values()), "digest": digest.hexdigest(), "originals": copied_originals}
    except Exception:
        qdrant.delete_collection(collection_name=target)
        if originals is not None:
            objects.delete_prefix(f"knowledge-snapshots/{snapshot_id}/")
        raise


def resolve(snapshot_id: str, kb_ids: list[str], gateway: GatewaySettings) -> str:
    if len(kb_ids) != 1:
        raise ValueError("one snapshot belongs to exactly one KB")
    collection = collection_name(snapshot_id)
    store.require_profile(collection, kb_ids)
    points, _ = store.client().scroll(collection_name=collection, limit=1, with_payload=True, with_vectors=False)
    payload = points[0].payload if points else {}
    if not payload or payload.get("snapshot_id") != snapshot_id or payload.get("snapshot_profile") != store.profile_collection(gateway) or payload.get("kb_id") != kb_ids[0]:
        raise ValueError("snapshot does not match the authorized KB and embedding profile")
    return collection


def discard(snapshot_id: str) -> None:
    collection = collection_name(snapshot_id)
    if store.client().collection_exists(collection):
        store.client().delete_collection(collection_name=collection)
    objects.delete_prefix(f"knowledge-snapshots/{snapshot_id}/")


def delete_knowledge_base(kb_id: str) -> None:
    pattern = re.compile(re.escape(store.settings.collection) + r"__s1_[0-9a-f]{32}$")
    qdrant = store.client()
    for item in qdrant.get_collections().collections:
        if not pattern.fullmatch(item.name):
            continue
        points, _ = qdrant.scroll(collection_name=item.name, limit=1, with_payload=True, with_vectors=False)
        if points and (points[0].payload or {}).get("kb_id") == kb_id:
            discard("kbs_" + item.name[-32:])
