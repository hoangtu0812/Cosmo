"""Retrieval metrics, defined once so two runs mean the same thing.

Every tuning decision left in the knowledge plane — how many candidates to
rerank, where to cut the score, how many chunks one document may contribute —
is currently a guess. It stays a guess until the same questions can be asked of
two configurations and the answers compared, which is what these four numbers
are for.

They are scored per document, not per chunk. Which chunk of a manual answers a
question is a detail of how it was split; whether the right manual came back at
all is the thing a curated question can honestly assert.
"""

from __future__ import annotations

import math
from typing import Mapping, Sequence


def recall_at_k(retrieved: Sequence[str], relevant: Sequence[str], k: int) -> float:
    """How much of what should have been found was found.

    The metric that matters most here: a reranker can reorder what retrieval
    handed it, but nothing downstream can recover a document that never came
    back at all.
    """
    wanted = set(relevant)
    if not wanted:
        return 0.0
    found = wanted.intersection(retrieved[:k])
    return len(found) / len(wanted)


def precision_at_k(retrieved: Sequence[str], relevant: Sequence[str], k: int) -> float:
    """How much of what was returned deserved to be.

    Read alongside recall rather than on its own: precision is trivially
    perfect for a system that returns one lucky result and nothing else.
    """
    if k <= 0 or not retrieved:
        return 0.0
    window = retrieved[:k]
    return len(set(relevant).intersection(window)) / len(window)


def reciprocal_rank(retrieved: Sequence[str], relevant: Sequence[str]) -> float:
    """One over the rank of the first correct result, or zero if there is none.

    Sensitive to position in a way recall is not: an answer at rank 1 and the
    same answer at rank 9 score the same recall and very different reciprocal
    ranks, which is the difference a reader actually experiences.
    """
    wanted = set(relevant)
    for position, document in enumerate(retrieved, start=1):
        if document in wanted:
            return 1.0 / position
    return 0.0


def ndcg_at_k(retrieved: Sequence[str], gains: Mapping[str, float], k: int) -> float:
    """Ranking quality when some documents are more relevant than others.

    Graded rather than binary, so a question whose answer is spread across a
    definitive procedure and a passing mention is not scored as though the two
    were interchangeable. Normalised against the best possible ordering, so
    1.0 means "nothing could have been ranked better".
    """
    if not gains or k <= 0:
        return 0.0

    def discounted(scores: Sequence[float]) -> float:
        return sum(score / math.log2(position + 2) for position, score in enumerate(scores))

    actual = discounted([float(gains.get(document, 0.0)) for document in retrieved[:k]])
    ideal = discounted(sorted((float(gain) for gain in gains.values()), reverse=True)[:k])
    return actual / ideal if ideal else 0.0


def unique(documents: Sequence[str]) -> list[str]:
    """Rank order with repeats removed, keeping the first appearance.

    Retrieval returns chunks and several of them can belong to one document.
    Scoring the raw list would let a document that contributed three chunks
    count three times, which flatters exactly the case diversity exists to
    prevent.
    """
    seen: set[str] = set()
    ordered: list[str] = []
    for document in documents:
        if document and document not in seen:
            seen.add(document)
            ordered.append(document)
    return ordered
