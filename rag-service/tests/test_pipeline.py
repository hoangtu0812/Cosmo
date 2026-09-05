"""Tests for the ingestion event contract.

The Go control plane treats a stream that ends without a terminal event as a
document that never finished, and marks it failed rather than ready. That
contract is what these tests hold in place.
"""

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import pipeline  # noqa: E402


@pytest.fixture
def stub(monkeypatch):
    """Replace object storage, the models and the vector store with stubs."""
    stored: dict = {}
    upserted: dict = {}

    monkeypatch.setattr(pipeline.objects, "put", lambda key, content, ct: stored.update(key=key))
    monkeypatch.setattr(pipeline.store, "delete_document", lambda document_id: None)
    monkeypatch.setattr(pipeline.store, "upsert", lambda chunks, encoded, **kwargs: upserted.update(n=len(chunks)))
    monkeypatch.setattr(pipeline.ml, "is_cold", lambda: False)
    monkeypatch.setattr(pipeline.ml, "encode", lambda texts, gateway: [object() for _ in texts])
    return {"stored": stored, "upserted": upserted}


def run(**overrides):
    arguments = {
        "content": b"# Title\n\nSome readable text about pump P-101.\n",
        "filename": "note.md",
        "content_type": "text/markdown",
        "kb_id": "kb_1",
        "document_id": "doc_1",
        "title": "Note",
        "document_version": 1,
        "effective_date": None,
        "gateway": pipeline.ml.GatewaySettings("https://gateway.example/v1", "", "embed", "rerank"),
    }
    arguments.update(overrides)
    return list(pipeline.run(**arguments))


class TestEventStream:
    def test_ends_with_done_on_success(self, stub):
        events = run()
        assert events[-1]["stage"] == "done"
        assert events[-1]["chunks"] > 0

    def test_reports_each_stage_in_order(self, stub):
        stages = [event["stage"] for event in run()]
        for stage in ("received", "stored", "parsing", "chunked", "embedding", "indexing", "done"):
            assert stage in stages, f"missing stage {stage}"
        assert stages.index("parsing") < stages.index("chunked") < stages.index("indexing")

    def test_every_event_carries_a_message_and_a_time(self, stub):
        for event in run():
            assert event["message"], f"{event['stage']} has no message"
            assert event["at"] > 0

    def test_embedding_reports_progress(self, stub):
        progress = [event for event in run() if event["stage"] == "embedding" and "total" in event]
        assert progress, "embedding must report how far it has got"
        assert progress[-1]["done"] == progress[-1]["total"]

    def test_stores_the_original_before_parsing(self, stub):
        events = run()
        assert stub["stored"]["key"].startswith("kb_1/doc_1")
        assert events.index(next(e for e in events if e["stage"] == "stored")) < events.index(
            next(e for e in events if e["stage"] == "parsing")
        )


class TestFailure:
    def test_unreadable_document_ends_with_error(self, stub):
        events = run(content=b"   \n\n  ", filename="empty.txt")
        assert events[-1]["stage"] == "error"
        assert "readable" in events[-1]["message"].lower()

    def test_a_raising_stage_ends_with_error_not_an_exception(self, stub, monkeypatch):
        # A crash mid-pipeline must still close the stream, or the caller waits
        # forever on a document that will never finish.
        def explode(*args, **kwargs):
            raise RuntimeError("index unavailable")

        monkeypatch.setattr(pipeline.store, "upsert", explode)
        events = run()
        assert events[-1]["stage"] == "error"
        assert "index unavailable" in events[-1]["message"]

    def test_nothing_is_indexed_when_parsing_finds_no_text(self, stub):
        run(content=b"   ", filename="empty.txt")
        assert "n" not in stub["upserted"], "an unreadable document must not reach the index"


class TestReindex:
    """A re-index reads the original where it already is.

    Sending the bytes back to the service that stored them costs a full copy of
    every document in both directions, and rewriting the object would put the
    only source the index can be rebuilt from at risk of a failed write.
    """

    def test_an_original_already_stored_is_not_written_again(self, stub, monkeypatch):
        def explode(*args, **kwargs):
            raise AssertionError("re-indexing must not rewrite the original")

        monkeypatch.setattr(pipeline.objects, "put", explode)
        events = run(storage_key="kb_1/doc_1.md")
        assert events[-1]["stage"] == "done"

    def test_existing_vectors_are_not_deleted_before_replacement(self, stub, monkeypatch):
        def fail(*args, **kwargs):
            raise AssertionError("must not delete searchable vectors first")
        monkeypatch.setattr(pipeline.store, "delete_document", fail)
        assert run(storage_key="kb_1/doc_1.md")[-1]["stage"] == "done"

    def test_the_original_keeps_its_key(self, stub, monkeypatch):
        monkeypatch.setattr(pipeline.objects, "put", lambda *args, **kwargs: None)
        events = run(storage_key="kb_1/doc_1.md")
        assert events[-1]["storage_key"] == "kb_1/doc_1.md"
