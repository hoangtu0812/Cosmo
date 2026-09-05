"""Tests for the retrieval logic we own.

Fusion, diversity, deduplication and the authority weighting decide what a
model is allowed to see and say, so they are tested directly rather than only
through the service. Nothing here touches the models or the vector store.
"""

import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import retrieve  # noqa: E402


def point(identifier: str, **payload):
    return SimpleNamespace(id=identifier, payload=payload)


def candidate(document_id: str, text: str = "", **payload):
    return retrieve.Candidate(
        point_id=f"{document_id}:{text[:8]}",
        payload={"document_id": document_id, "text": text, **payload},
    )


class TestFusion:
    def test_combines_by_rank_not_by_score(self):
        """A chunk ranked high in both lists must beat one that tops only one."""
        dense = [point("a"), point("b"), point("c")]
        sparse = [point("c"), point("a"), point("d")]

        fused = retrieve._fuse([("dense:kb", dense), ("sparse:kb", sparse)])

        order = sorted(fused.values(), key=lambda item: item.fused, reverse=True)
        assert order[0].point_id == "a"  # ranks 1 and 2 in the two lists
        assert {item.point_id for item in order} == {"a", "b", "c", "d"}

    def test_records_which_retrievers_matched(self):
        fused = retrieve._fuse([("dense:kb1", [point("a")]), ("sparse:kb1", [point("a")])])
        assert sorted(fused["a"].sources) == ["dense:kb1", "sparse:kb1"]

    def test_a_single_list_still_ranks(self):
        fused = retrieve._fuse([("dense:kb", [point("a"), point("b")])])
        assert fused["a"].fused > fused["b"].fused


class TestDiversity:
    def test_caps_chunks_per_document(self):
        candidates = [candidate("doc1", f"chunk {index}") for index in range(5)]
        kept = retrieve._diversify(candidates, limit=10, per_document=2)
        assert len(kept) == 5, "overflow tops up once every document had a turn"
        assert [item.payload["document_id"] for item in kept[:2]] == ["doc1", "doc1"]

    def test_prefers_breadth_across_documents(self):
        candidates = [
            candidate("doc1", "a"),
            candidate("doc1", "b"),
            candidate("doc1", "c"),
            candidate("doc2", "d"),
        ]
        kept = retrieve._diversify(candidates, limit=3, per_document=2)
        documents = [item.payload["document_id"] for item in kept]
        assert "doc2" in documents, "one document must not fill the whole result"

    def test_respects_the_limit(self):
        candidates = [candidate(f"doc{index}", "text") for index in range(10)]
        assert len(retrieve._diversify(candidates, limit=4, per_document=3)) == 4


class TestDeduplication:
    def test_drops_repeated_text(self):
        candidates = [candidate("doc1", "The pump trips on high vibration."),
                      candidate("doc1", "The  pump trips on high   vibration.")]
        assert len(retrieve._deduplicate(candidates)) == 1, "whitespace is not a difference"

    def test_keeps_distinct_text(self):
        candidates = [candidate("doc1", "Alpha"), candidate("doc2", "Beta")]
        assert len(retrieve._deduplicate(candidates)) == 2

    def test_keeps_conflicting_tails_after_a_common_prefix(self):
        prefix = "Common reference text. " * 30
        candidates = [candidate("doc1", prefix + "Approval is required."),
                      candidate("doc1", prefix + "Approval is NOT required.")]
        assert len(retrieve._deduplicate(candidates)) == 2

    def test_keeps_distinct_citation_sources_and_case_sensitive_codes(self):
        candidates = [candidate("doc1", "ABC", kb_id="one"), candidate("doc2", "ABC", kb_id="one"),
                      candidate("doc1", "ABC", kb_id="two"), candidate("doc1", "abc", kb_id="one")]
        assert len(retrieve._deduplicate(candidates)) == 4

    def test_keeps_different_pages_of_the_same_document(self):
        candidates = [candidate("doc1", "Text", page="1"), candidate("doc1", "Text", page="2")]
        assert len(retrieve._deduplicate(candidates)) == 2


class TestAuthority:
    def test_recent_documents_outrank_old_ones(self):
        recent = datetime.now(timezone.utc) - timedelta(days=30)
        old = datetime.now(timezone.utc) - timedelta(days=365 * 12)
        assert retrieve._authority({"effective_date": recent.isoformat()}) > retrieve._authority(
            {"effective_date": old.isoformat()}
        )

    def test_missing_metadata_is_neutral(self):
        assert retrieve._authority({}) == 1.0

    def test_unparseable_date_does_not_raise(self):
        assert retrieve._authority({"effective_date": "sometime last year"}) == 1.0

    def test_old_documents_are_discounted_not_discarded(self):
        ancient = datetime.now(timezone.utc) - timedelta(days=365 * 40)
        assert retrieve._authority({"effective_date": ancient.isoformat()}) >= 0.85


class TestAccess:
    def test_unauthorized_text_never_reaches_reranker(self, monkeypatch):
        from app.models import Encoded, GatewaySettings
        monkeypatch.setattr(retrieve.store, "require_profile", lambda *args: None)
        monkeypatch.setattr(retrieve.ml, "encode", lambda *args: [Encoded([1.0, 0.0])])
        def dense(kbs, *args, **kwargs):
            if kbs == ["one"]:
                return [point("bad", kb_id="secret", document_id="secret", text="PRIVATE"),
                        point("wrong-branch", kb_id="two", document_id="wrong", text="WRONG BRANCH"),
                        point("ok", kb_id="one", document_id="public", text="Allowed evidence")]
            return []
        monkeypatch.setattr(retrieve.store, "search_dense", dense)
        def rerank(query, texts, gateway):
            assert texts == ["Allowed evidence"]
            return [1.0]
        monkeypatch.setattr(retrieve.ml, "rerank", rerank)
        results = retrieve.search("query", ["one", "two"], gateway=GatewaySettings("https://gateway.invalid", "", "embed", "rank"), retrieval_mode="semantic")
        assert len(results) == 1 and results[0]["document_id"] == "public"

    def test_no_authorised_kb_returns_nothing(self):
        """An empty allow-list means nothing, never everything."""
        assert retrieve.search("anything", []) == []

    def test_blank_query_returns_nothing(self):
        assert retrieve.search("   ", ["kb_1"]) == []
