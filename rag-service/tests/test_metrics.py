"""Tests for the metrics every tuning decision will be argued from.

A metric that is subtly wrong is worse than no metric: it does not stop the
guessing, it launders it. These pin each definition to a case worked out by
hand.
"""

import math
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import metrics  # noqa: E402


class TestRecall:
    def test_everything_found(self):
        assert metrics.recall_at_k(["a", "b"], ["a", "b"], 10) == 1.0

    def test_half_found(self):
        assert metrics.recall_at_k(["a", "x"], ["a", "b"], 10) == 0.5

    def test_a_result_past_k_does_not_count(self):
        # The cut is the whole point: a document at rank 20 is not something
        # the model was ever shown.
        assert metrics.recall_at_k(["x", "x", "a"], ["a"], 2) == 0.0

    def test_no_expected_documents_scores_zero(self):
        assert metrics.recall_at_k(["a"], [], 10) == 0.0


class TestPrecision:
    def test_counts_against_what_was_returned(self):
        assert metrics.precision_at_k(["a", "x", "y", "z"], ["a"], 4) == 0.25

    def test_a_short_result_list_is_not_padded(self):
        # Two results, one right, is 0.5 — not 0.1 against a k of 10.
        assert metrics.precision_at_k(["a", "x"], ["a"], 10) == 0.5

    def test_nothing_returned_scores_zero(self):
        assert metrics.precision_at_k([], ["a"], 10) == 0.0


class TestReciprocalRank:
    def test_first_place(self):
        assert metrics.reciprocal_rank(["a", "b"], ["a"]) == 1.0

    def test_third_place(self):
        assert metrics.reciprocal_rank(["x", "y", "a"], ["a"]) == 1 / 3

    def test_nothing_relevant_scores_zero(self):
        assert metrics.reciprocal_rank(["x", "y"], ["a"]) == 0.0


class TestNDCG:
    def test_the_best_possible_order_scores_one(self):
        assert metrics.ndcg_at_k(["a", "b"], {"a": 3, "b": 1}, 10) == 1.0

    def test_the_wrong_order_scores_less(self):
        assert metrics.ndcg_at_k(["b", "a"], {"a": 3, "b": 1}, 10) < 1.0

    def test_grading_is_respected(self):
        """Returning the definitive document must beat the passing mention."""
        definitive = metrics.ndcg_at_k(["a"], {"a": 3, "b": 1}, 1)
        passing = metrics.ndcg_at_k(["b"], {"a": 3, "b": 1}, 1)
        assert definitive > passing

    def test_matches_a_hand_worked_value(self):
        # One relevant document at rank 2: DCG = 1/log2(3), IDCG = 1/log2(2).
        assert metrics.ndcg_at_k(["x", "a"], {"a": 1}, 10) == 1 / math.log2(3)

    def test_no_gains_scores_zero(self):
        assert metrics.ndcg_at_k(["a"], {}, 10) == 0.0


class TestUnique:
    def test_several_chunks_of_one_document_count_once(self):
        # Otherwise a document that contributed three chunks is credited three
        # times, flattering exactly the case diversity exists to prevent.
        assert metrics.unique(["a", "a", "b", "a"]) == ["a", "b"]

    def test_rank_order_is_kept(self):
        assert metrics.unique(["b", "a", "b"]) == ["b", "a"]

    def test_empty_identifiers_are_dropped(self):
        assert metrics.unique(["", "a", ""]) == ["a"]
