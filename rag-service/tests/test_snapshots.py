from dataclasses import replace

import pytest
from qdrant_client import QdrantClient

from app import models, retrieve, snapshots, store, objects

SNAPSHOT = "kbs_" + "a" * 32
GATEWAY = models.GatewaySettings("https://gateway.invalid/v1", "secret", "embed", "rerank", "owner")


@pytest.fixture
def index(monkeypatch):
    qdrant = QdrantClient(":memory:")
    monkeypatch.setattr(store, "client", lambda: qdrant)
    monkeypatch.setattr(objects, "delete_prefix", lambda prefix: None)
    monkeypatch.setattr(retrieve.ml, "encode", lambda *args: [models.Encoded([1., 0.])])
    write("original evidence")
    yield qdrant
    qdrant.close()


def write(text):
    store.upsert([dict(kb_id="kb", document_id="doc", chunk_index=0, text=text, status="active")],
                 [models.Encoded([1., 0.])], collection=store.profile_collection(GATEWAY))


def test_snapshot_survives_live_replacement_and_document_deletion(index):
    result = snapshots.create(SNAPSHOT, "kb", GATEWAY, {"doc": 1})
    assert result["chunks"] == 1 and len(result["digest"]) == 64
    write("new evidence")
    for mode in ("hybrid", "keyword", "semantic"):
        found = retrieve.search("evidence", ["kb"], gateway=GATEWAY, snapshot_id=SNAPSHOT,
                                retrieval_mode=mode, rerank_enabled=False)
        assert found[0]["text"] == "original evidence"
    store.delete_document("doc")
    assert retrieve.search("evidence", ["kb"], gateway=GATEWAY, snapshot_id=SNAPSHOT,
                           rerank_enabled=False)[0]["text"] == "original evidence"
    snapshots.delete_knowledge_base("other")
    assert index.collection_exists(snapshots.collection_name(SNAPSHOT))
    snapshots.delete_knowledge_base("kb")
    assert not index.collection_exists(snapshots.collection_name(SNAPSHOT))


def test_snapshot_cannot_be_overwritten_or_used_with_other_profile(index):
    snapshots.create(SNAPSHOT, "kb", GATEWAY, {"doc": 1})
    with pytest.raises(Exception):
        snapshots.create(SNAPSHOT, "kb", GATEWAY, {"doc": 1})
    assert snapshots.resolve(SNAPSHOT, ["kb"], replace(GATEWAY, api_key="rotated"))
    for kb_ids, gateway in [(["other"], GATEWAY), (["kb", "other"], GATEWAY),
                            (["kb"], replace(GATEWAY, embedding_model="other"))]:
        with pytest.raises((ValueError, store.ProfileNotIndexed)):
            snapshots.resolve(SNAPSHOT, kb_ids, gateway)


@pytest.mark.parametrize("manifest", [{"doc": 2}, {"other": 1}, {"doc": 1, "missing": 1}])
def test_incomplete_copy_is_removed(index, manifest):
    with pytest.raises(ValueError):
        snapshots.create(SNAPSHOT, "kb", GATEWAY, manifest)
    assert not index.collection_exists(snapshots.collection_name(SNAPSHOT))
    assert store.inspect_document("doc", collection=store.profile_collection(GATEWAY))["indexed"]


def test_copy_write_failure_removes_partial_collection(index, monkeypatch):
    def fail(**kwargs):
        raise RuntimeError("injected write failure")
    monkeypatch.setattr(index, "upsert", fail)
    with pytest.raises(RuntimeError, match="injected"):
        snapshots.create(SNAPSHOT, "kb", GATEWAY, {"doc": 1})
    assert not index.collection_exists(snapshots.collection_name(SNAPSHOT))


def test_original_copy_retained_and_discarded_with_snapshot(index, monkeypatch):
    files = {"live-file": b"original"}
    def copy(source, target):
        files[target] = files[source]
        return {"size_bytes": len(files[target]), "etag": "test"}
    def delete(prefix):
        for key in list(files):
            if key.startswith(prefix): del files[key]
    monkeypatch.setattr(objects, "copy_original", copy)
    monkeypatch.setattr(objects, "delete_prefix", delete)
    original = {"storage_key": "live-file", "size_bytes": 8, "filename": "test.txt", "content_type": "text/plain"}
    result = snapshots.create(SNAPSHOT, "kb", GATEWAY, {"doc": 1}, {"doc": original})
    del files["live-file"]
    assert files[result["originals"]["doc"]["storage_key"]] == b"original"
    snapshots.discard(SNAPSHOT)
    assert not files


def test_missing_original_prevents_publication_and_cleans(index, monkeypatch):
    cleaned = []
    def fail(*args): raise RuntimeError("missing original")
    monkeypatch.setattr(objects, "copy_original", fail)
    monkeypatch.setattr(objects, "delete_prefix", cleaned.append)
    with pytest.raises(RuntimeError, match="missing original"):
        snapshots.create(SNAPSHOT, "kb", GATEWAY, {"doc": 1}, {"doc": {"storage_key": "missing"}})
    assert cleaned == [f"knowledge-snapshots/{SNAPSHOT}/"]
    assert not index.collection_exists(snapshots.collection_name(SNAPSHOT))
