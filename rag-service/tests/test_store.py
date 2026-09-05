"""Safety contracts for an empty vector index."""

import sys
from pathlib import Path

import pytest
from qdrant_client import QdrantClient

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import store  # noqa: E402
from app.models import Encoded  # noqa: E402


class EmptyQdrant:
    def __init__(self):
        self.deleted = False

    def collection_exists(self, _collection):
        return False

    def delete(self, **_kwargs):
        self.deleted = True

    def delete_collection(self, **_kwargs):
        self.deleted = True


def test_deleting_document_is_safe_after_index_reset(monkeypatch):
    qdrant = EmptyQdrant()
    monkeypatch.setattr(store, "client", lambda: qdrant)

    store.delete_document("doc_1")

    assert qdrant.deleted is False


def test_reset_is_safe_when_collection_is_already_absent(monkeypatch):
    qdrant = EmptyQdrant()
    monkeypatch.setattr(store, "client", lambda: qdrant)

    store.reset_collection()

    assert qdrant.deleted is False


@pytest.fixture
def indexed(monkeypatch):
    qdrant = QdrantClient(":memory:")
    monkeypatch.setattr(store, "client", lambda: qdrant)
    chunks = [dict(kb_id="kb_1", document_id="doc_1", chunk_index=i, text=f"old {i}", status="active") for i in range(3)]
    store.upsert(chunks, [Encoded([1.0, 0.0]) for _ in chunks])
    yield qdrant, chunks
    qdrant.close()


def test_replacement_prunes_only_obsolete_chunks(indexed):
    _, chunks = indexed
    unrelated = dict(chunks[0], document_id="other", kb_id="other_kb")
    store.upsert([unrelated], [Encoded([1.0, 0.0])])
    store.upsert([dict(chunks[0], text="new")], [Encoded([0.0, 1.0])])
    assert [c["text"] for c in store.inspect_document("doc_1")["chunks"]] == ["new"]
    assert store.inspect_document("other")["indexed"]


def test_failed_upsert_does_not_delete_previous_document(indexed, monkeypatch):
    qdrant, chunks = indexed
    def fail(**kwargs):
        raise RuntimeError("write unavailable")
    monkeypatch.setattr(qdrant, "upsert", fail)
    with pytest.raises(RuntimeError, match="write unavailable"):
        store.upsert([dict(chunks[0], text="new")], [Encoded([0.0, 1.0])])
    assert [c["text"] for c in store.inspect_document("doc_1")["chunks"]] == ["old 0", "old 1", "old 2"]


@pytest.mark.parametrize("vectors", [[], [Encoded([1.0, 0.0, 0.0])], [Encoded([float("nan"), 0.0])]])
def test_invalid_replacement_keeps_old_document(indexed, vectors):
    _, chunks = indexed
    with pytest.raises((ValueError, RuntimeError)):
        store.upsert([chunks[0]], vectors)
    assert len(store.inspect_document("doc_1")["chunks"]) == 3


def test_cleanup_failure_can_be_retried(indexed, monkeypatch):
    qdrant, chunks = indexed
    delete = qdrant.delete
    def fail(**kwargs):
        raise RuntimeError("cleanup unavailable")
    monkeypatch.setattr(qdrant, "delete", fail)
    replacement = [dict(chunks[0], text="new")]
    vectors = [Encoded([0.0, 1.0])]
    with pytest.raises(RuntimeError, match="cleanup unavailable"):
        store.upsert(replacement, vectors)
    assert store.inspect_document("doc_1")["indexed"]
    monkeypatch.setattr(qdrant, "delete", delete)
    store.upsert(replacement, vectors)
    assert [c["text"] for c in store.inspect_document("doc_1")["chunks"]] == ["new"]
