"""Tests for the evaluation runner.

The runner's job is to be trustworthy: a question set that cannot be scored has
to be refused by name rather than averaged into a number, and one failing
question must not take the run down with it.
"""

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import evaluate  # noqa: E402


def write(tmp_path, payload) -> Path:
    path = tmp_path / "questions.json"
    path.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
    return path


class TestLoading:
    def test_a_list_of_relevant_documents_loads_as_binary_gains(self, tmp_path):
        path = write(tmp_path, {"questions": [
            {"id": "q1", "query": "quy trình hàn", "kb_ids": ["kb1"], "relevant": ["doc1", "doc2"]},
        ]})
        question = evaluate.load(path)[0]
        assert question.gains == {"doc1": 1.0, "doc2": 1.0}
        assert sorted(question.relevant) == ["doc1", "doc2"]

    def test_graded_relevance_loads_as_given(self, tmp_path):
        path = write(tmp_path, {"questions": [
            {"id": "q1", "query": "bơm", "kb_ids": ["kb1"], "relevant": {"doc1": 3, "doc2": 1}},
        ]})
        assert evaluate.load(path)[0].gains == {"doc1": 3.0, "doc2": 1.0}

    def test_a_question_with_no_query_is_refused(self, tmp_path):
        path = write(tmp_path, {"questions": [{"id": "q1", "kb_ids": ["kb1"], "relevant": ["doc1"]}]})
        with pytest.raises(ValueError, match="q1"):
            evaluate.load(path)

    def test_a_question_with_no_relevant_document_is_refused(self, tmp_path):
        # It would score zero however good retrieval is, dragging the average
        # down for a reason that has nothing to do with retrieval.
        path = write(tmp_path, {"questions": [{"id": "q1", "query": "x", "kb_ids": ["kb1"], "relevant": []}]})
        with pytest.raises(ValueError, match="q1"):
            evaluate.load(path)

    def test_a_question_with_no_knowledge_base_is_refused(self, tmp_path):
        path = write(tmp_path, {"questions": [{"id": "q1", "query": "x", "relevant": ["doc1"]}]})
        with pytest.raises(ValueError, match="q1"):
            evaluate.load(path)

    def test_an_empty_set_is_refused(self, tmp_path):
        with pytest.raises(ValueError):
            evaluate.load(write(tmp_path, {"questions": []}))


def question(**overrides) -> evaluate.Question:
    values = {"id": "q1", "query": "quy trình", "kb_ids": ["kb1"], "gains": {"doc1": 1.0}}
    values.update(overrides)
    return evaluate.Question(**values)


def passages(*documents, matched=("dense",)):
    return [{"document_id": document, "matched": list(matched)} for document in documents]


class TestMeasuring:
    def test_scores_what_retrieval_returned(self, monkeypatch):
        monkeypatch.setattr(evaluate.retrieve, "search", lambda *a, **k: passages("doc1", "other"))
        outcome = evaluate.measure(question(), 10)
        assert outcome.recall == 1.0
        assert outcome.reciprocal_rank == 1.0
        assert outcome.retrievers == ["dense"]

    def test_repeated_chunks_of_one_document_count_once(self, monkeypatch):
        monkeypatch.setattr(evaluate.retrieve, "search", lambda *a, **k: passages("doc1", "doc1", "other"))
        assert evaluate.measure(question(), 10).retrieved == ["doc1", "other"]

    def test_a_failing_question_is_recorded_not_raised(self, monkeypatch):
        def explode(*args, **kwargs):
            raise RuntimeError("gateway unreachable")

        monkeypatch.setattr(evaluate.retrieve, "search", explode)
        outcome = evaluate.measure(question(), 10)
        assert outcome.error == "gateway unreachable"
        assert outcome.recall == 0.0

    def test_records_which_retrievers_contributed(self, monkeypatch):
        monkeypatch.setattr(evaluate.retrieve, "search",
                            lambda *a, **k: passages("doc1", matched=("dense", "sparse")))
        assert evaluate.measure(question(), 10).retrievers == ["dense", "sparse"]


class TestSummary:
    def test_a_failed_question_is_excluded_from_the_averages(self, monkeypatch):
        # Averaging a zero from an unreachable gateway would read as a
        # retrieval regression that never happened.
        good = evaluate.Outcome(question=question(), retrieved=["doc1"], recall=1.0, ndcg=1.0)
        bad = evaluate.Outcome(question=question(id="q2"), retrieved=[], error="boom")
        report = evaluate.summarise([good, bad], 10)
        assert report["recall"] == 1.0
        assert report["scored"] == 1
        assert report["failed"] == 1

    def test_counts_questions_the_lexical_half_reached(self, outcomes=None):
        lexical = evaluate.Outcome(question=question(), retrieved=["doc1"], retrievers=["dense", "sparse"])
        dense = evaluate.Outcome(question=question(id="q2"), retrieved=["doc1"], retrievers=["dense"])
        assert evaluate.summarise([lexical, dense], 10)["lexical_questions"] == 1

    def test_renders_a_delta_against_a_baseline(self):
        report = evaluate.summarise([evaluate.Outcome(question=question(), retrieved=["doc1"], recall=0.8)], 10)
        rendered = evaluate.render(report, {"recall": 0.6, "precision": 0.0, "mrr": 0.0, "ndcg": 0.0})
        assert "+0.2000" in rendered
