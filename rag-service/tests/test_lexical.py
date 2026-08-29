"""Tests for the lexical half of retrieval.

This exists to catch what a dense model cannot: the document codes and part
numbers a maintenance question is actually about. The tests are mostly about
tokenisation, because that is where such a string is either preserved or
destroyed.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import lexical  # noqa: E402


class TestTerms:
    def test_a_document_code_survives_whole(self):
        assert "qt-atsk-01" in lexical.terms("Xem QT-ATSK-01 truoc khi thao tac")

    def test_a_document_code_is_also_searchable_in_parts(self):
        # Someone who remembers "ATSK 01" but not the prefix still has to reach
        # the document that carries the code.
        found = lexical.terms("QT-ATSK-01")
        assert {"qt-atsk-01", "qt", "atsk", "01"} <= set(found)

    def test_an_equipment_tag_is_one_term(self):
        assert "p-101a" in lexical.terms("Bom P-101A dang chay")

    def test_vietnamese_keeps_its_diacritics(self):
        found = lexical.terms("Quy trình bảo dưỡng")
        assert found == ["quy", "trình", "bảo", "dưỡng"]

    def test_case_does_not_matter(self):
        assert lexical.terms("Bơm P-101A") == lexical.terms("bơm p-101a")

    def test_punctuation_is_not_a_term(self):
        assert lexical.terms("Bước 1: khoá van.") == ["bước", "1", "khoá", "van"]


class TestPassageWeights:
    def test_an_empty_passage_has_no_weights(self):
        assert lexical.encode("   ") == {}

    def test_a_repeated_term_weighs_more(self):
        once = lexical.encode("van")
        twice = lexical.encode("van van")
        index = next(iter(once))
        assert twice[index] > once[index]

    def test_repetition_saturates(self):
        """Ten mentions must not be ten times one, or one word wins outright."""
        once = lexical.encode("van")
        many = lexical.encode(" ".join(["van"] * 10))
        index = next(iter(once))
        assert many[index] < once[index] * 10

    def test_a_longer_passage_dilutes_the_same_term(self):
        short = lexical.encode("van")
        long = lexical.encode("van " + " ".join(f"tu{position}" for position in range(400)))
        index = next(iter(short))
        assert long[index] < short[index]

    def test_the_slot_for_a_term_is_stable(self):
        # The index is written to Qdrant and read back by another process, so
        # it has to mean the same thing every time it is computed.
        assert set(lexical.encode("p-101a")) == set(lexical.query("p-101a"))


class TestQueryWeights:
    def test_every_asked_term_weighs_the_same(self):
        # What separates a rare code from a common word is IDF, and IDF is the
        # index's to apply.
        assert set(lexical.query("bom p-101a").values()) == {1.0}

    def test_a_term_asked_twice_is_asked_once(self):
        assert len(lexical.query("van van van")) == 1

    def test_an_empty_query_asks_for_nothing(self):
        assert lexical.query("") == {}
