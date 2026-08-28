"""Safety contracts for an empty vector index."""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import store  # noqa: E402


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
