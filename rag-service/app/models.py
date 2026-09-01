"""Request-scoped gateway-backed embeddings and reranking.

The knowledge plane never stores a workspace credential. The control plane
passes the owning workspace's gateway configuration with each request and the
immutable value is threaded through the pipeline. It must not live in module
state: two workspaces can ingest at the same time.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Sequence


@dataclass(frozen=True)
class GatewaySettings:
    base_url: str
    api_key: str
    embedding_model: str
    reranker_model: str


def gateway_settings(
    embedding_model: str | None,
    reranker_model: str | None,
    gateway_base_url: str | None,
    gateway_api_key: str | None,
) -> GatewaySettings:
    """Validate and return one trusted workspace gateway configuration."""
    settings = GatewaySettings(
        base_url=(gateway_base_url or "").rstrip("/"),
        api_key=gateway_api_key or "",
        embedding_model=embedding_model or "",
        reranker_model=reranker_model or "",
    )
    if not settings.base_url:
        raise RuntimeError("Workspace Model Gateway is not configured")
    if not settings.embedding_model:
        raise RuntimeError("Embedding model is not configured")
    return settings


def _post(gateway: GatewaySettings, path: str, payload: dict) -> dict:
    request = urllib.request.Request(
        gateway.base_url + path,
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            **({"Authorization": f"Bearer {gateway.api_key}"} if gateway.api_key else {}),
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=90) as response:
            body = response.read()
    except urllib.error.HTTPError as error:
        detail = error.read(2048).decode("utf-8", errors="replace").strip()
        raise RuntimeError(f"Workspace Model Gateway returned {error.code}: {detail}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"Workspace Model Gateway is unreachable: {error.reason}") from error

    try:
        decoded = json.loads(body)
    except json.JSONDecodeError as error:
        raise RuntimeError("Workspace Model Gateway returned invalid JSON") from error
    if not isinstance(decoded, dict):
        raise RuntimeError("Workspace Model Gateway returned an invalid response")
    return decoded


def is_cold() -> bool:
    """The data plane no longer has a local model to warm up."""
    return False


class Encoded:
    """One gateway-provided dense vector, and only that.

    A generic OpenAI-compatible embeddings API has no lexical weights to give.
    The lexical half of retrieval is therefore computed locally in `lexical`
    rather than fabricated from a call this gateway cannot make.
    """

    __slots__ = ("dense",)

    def __init__(self, dense: list[float]):
        self.dense = dense


def encode(texts: Sequence[str], gateway: GatewaySettings) -> list[Encoded]:
    if not texts:
        return []
    response = _post(gateway, "/embeddings", {"model": gateway.embedding_model, "input": list(texts)})
    data = response.get("data")
    if not isinstance(data, list) or len(data) != len(texts):
        raise RuntimeError("Workspace Model Gateway returned incomplete embeddings")

    vectors: list[Encoded | None] = [None] * len(texts)
    for position, item in enumerate(data):
        if not isinstance(item, dict):
            raise RuntimeError("Workspace Model Gateway returned an invalid embedding")
        index = item.get("index", position)
        embedding = item.get("embedding")
        if not isinstance(index, int) or index < 0 or index >= len(vectors) or not isinstance(embedding, list) or not embedding:
            raise RuntimeError("Workspace Model Gateway returned an invalid embedding")
        try:
            vectors[index] = Encoded([float(value) for value in embedding])
        except (TypeError, ValueError) as error:
            raise RuntimeError("Workspace Model Gateway returned a non-numeric embedding") from error
    if any(vector is None for vector in vectors):
        raise RuntimeError("Workspace Model Gateway returned incomplete embeddings")
    return [vector for vector in vectors if vector is not None]


def rerank(query: str, passages: Sequence[str], gateway: GatewaySettings) -> list[float]:
    if not passages:
        return []
    if not gateway.reranker_model:
        raise RuntimeError("Reranker model is not configured")
    response = _post(
        gateway,
        "/rerank",
        {"model": gateway.reranker_model, "query": query, "documents": list(passages), "top_n": len(passages)},
    )
    results = response.get("results")
    if not isinstance(results, list):
        raise RuntimeError("Workspace Model Gateway returned invalid rerank results")

    scores = [0.0] * len(passages)
    found = set()
    for item in results:
        if not isinstance(item, dict):
            continue
        index = item.get("index")
        score = item.get("relevance_score", item.get("score"))
        if not isinstance(index, int) or index < 0 or index >= len(scores):
            continue
        try:
            scores[index] = float(score)
            found.add(index)
        except (TypeError, ValueError):
            continue
    if not found:
        raise RuntimeError("Workspace Model Gateway returned invalid rerank results")
    return scores
