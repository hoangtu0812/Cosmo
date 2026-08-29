"""Lexical term weights, for the half of retrieval embeddings are bad at.

A dense model is good at meaning and poor at identifiers. "QT-ATSK-01" and
"P-101A" are exactly the strings a maintenance question turns on, and a model
that has never seen them places them near nothing in particular. BM25 finds
them by matching the characters, which is why every chunk carries a lexical
vector beside its dense one and fusion weighs the two lists together.

Qdrant applies IDF itself — the sparse vector is configured with the IDF
modifier — so what is stored here is BM25's term-frequency half and what is
sent at query time is the term's presence. Keeping the corpus statistics in the
index is what lets this module stay stateless: no document frequencies to
maintain, and no drift between what was indexed and what is searched.

No stemmer. Vietnamese does not inflect, so there is nothing to strip, and a
stemmer built for English would only damage the identifiers this exists to
catch.
"""

from __future__ import annotations

import re
import zlib

from .config import settings

# A token is a run of word characters, and a run joined by the separators that
# hold a document code together. Splitting "QT-ATSK-01" on the hyphen would
# turn the most searchable string in the corpus into three common fragments.
_TOKEN = re.compile(r"\w+(?:[-_./]\w+)*", re.UNICODE)
_SEPARATOR = re.compile(r"[-_./]")

# BM25's saturation constants, at the values the original paper settles on.
K1 = 1.2
B = 0.75


def terms(text: str) -> list[str]:
    """Every term a passage should be findable by.

    A code is emitted whole and in parts: someone who remembers "ATSK 01" but
    not the prefix should still reach the document that carries it.
    """
    found: list[str] = []
    for match in _TOKEN.findall(text.casefold()):
        found.append(match)
        if _SEPARATOR.search(match):
            found.extend(part for part in _SEPARATOR.split(match) if part)
    return found


def _index(term: str) -> int:
    """A stable 32-bit slot for a term.

    CRC32 rather than hash(): the index is written to Qdrant and read back by a
    different process, so it has to mean the same thing in both.
    """
    return zlib.crc32(term.encode("utf-8"))


def encode(text: str) -> dict[int, float]:
    """The stored half of BM25: how much this passage is about each term.

    Length normalisation uses a configured average rather than a measured one.
    Measuring it would mean holding corpus statistics that go stale the moment
    a document is added, for a correction that only bends the saturation curve.
    """
    counts: dict[int, float] = {}
    tokens = terms(text)
    if not tokens:
        return {}

    frequencies: dict[str, int] = {}
    for token in tokens:
        frequencies[token] = frequencies.get(token, 0) + 1

    normalisation = K1 * (1 - B + B * (len(tokens) / max(settings.average_passage_terms, 1)))
    for token, frequency in frequencies.items():
        counts[_index(token)] = frequency * (K1 + 1) / (frequency + normalisation)
    return counts


def query(text: str) -> dict[int, float]:
    """The query half: which terms were asked for.

    Every term weighs the same here. What separates a rare document code from
    the word "the" is IDF, and IDF is the index's to apply.
    """
    return {_index(token): 1.0 for token in set(terms(text))}
