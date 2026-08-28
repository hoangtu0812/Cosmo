"""BGE-M3 embeddings and the BGE reranker, loaded lazily.

Both models are large. Loading them at import time would make the container
fail its healthcheck while the weights download, so each is loaded on first
use and then held for the process lifetime.
"""

from __future__ import annotations

import logging
import threading
from typing import Sequence

from .config import settings

logger = logging.getLogger(__name__)

_embedder = None
_reranker = None
_embedder_lock = threading.Lock()
_reranker_lock = threading.Lock()
_settings_lock = threading.Lock()
_embedding_model = settings.embedding_model
_reranker_model = settings.reranker_model


def configure(embedding_model: str | None, reranker_model: str | None) -> None:
    """Apply the control plane's model selection to the next operation.

    Model instances are discarded only when their identifier changes. This
    makes a saved Admin setting effective immediately while retaining a warm
    model when normal jobs use the same configuration.
    """
    global _embedder, _reranker, _embedding_model, _reranker_model
    with _settings_lock:
        if embedding_model and embedding_model != _embedding_model:
            logger.info("switching embedding model from %s to %s", _embedding_model, embedding_model)
            _embedding_model = embedding_model
            _embedder = None
        if reranker_model and reranker_model != _reranker_model:
            logger.info("switching reranker from %s to %s", _reranker_model, reranker_model)
            _reranker_model = reranker_model
            _reranker = None


def embedder():
    global _embedder
    if _embedder is None:
        with _embedder_lock:
            if _embedder is None:
                from FlagEmbedding import BGEM3FlagModel

                logger.info("loading embedding model %s", _embedding_model)
                _embedder = BGEM3FlagModel(
                    _embedding_model,
                    cache_dir=settings.model_cache,
                    use_fp16=False,
                )
    return _embedder


def reranker():
    global _reranker
    if _reranker is None:
        with _reranker_lock:
            if _reranker is None:
                from FlagEmbedding import FlagReranker

                logger.info("loading reranker %s", _reranker_model)
                _reranker = FlagReranker(
                    _reranker_model,
                    cache_dir=settings.model_cache,
                    use_fp16=False,
                )
    return _reranker


def is_cold() -> bool:
    """Whether the embedding model still has to be loaded.

    Used only to phrase progress honestly: the first ingestion after a restart
    spends minutes downloading weights, and saying so beats a silent pause.
    """
    return _embedder is None


class Encoded:
    """One text's dense vector plus its sparse lexical weights."""

    __slots__ = ("dense", "sparse")

    def __init__(self, dense: list[float], sparse: dict[int, float]):
        self.dense = dense
        self.sparse = sparse


def encode(texts: Sequence[str]) -> list[Encoded]:
    """Encode texts into dense and sparse representations in a single pass.

    BGE-M3 produces both from one forward pass, which is why it is worth the
    weight: the sparse side carries the exact tokens (equipment tags, work
    order numbers) that dense embeddings blur away.
    """
    if not texts:
        return []

    output = embedder().encode(
        list(texts),
        return_dense=True,
        return_sparse=True,
        return_colbert_vecs=False,
    )

    result: list[Encoded] = []
    for dense, sparse in zip(output["dense_vecs"], output["lexical_weights"]):
        weights = {int(token): float(weight) for token, weight in sparse.items() if float(weight) > 0}
        result.append(Encoded(dense=[float(value) for value in dense], sparse=weights))
    return result


def rerank(query: str, passages: Sequence[str]) -> list[float]:
    """Score each passage against the query with the cross-encoder."""
    if not passages:
        return []
    scores = reranker().compute_score([[query, passage] for passage in passages], normalize=True)
    if isinstance(scores, float):
        return [scores]
    return [float(score) for score in scores]
