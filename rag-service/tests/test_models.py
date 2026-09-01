"""The gateway adapter must never fall back to a local model runtime."""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import models  # noqa: E402


class Response:
    def __init__(self, payload):
        self.payload = payload

    def read(self):
        return json.dumps(self.payload).encode("utf-8")

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False


def configure():
    return models.gateway_settings("qwen3-embedding", "qwen3-reranker", "https://gateway.example/v1", "secret")


def test_embeddings_use_system_gateway(monkeypatch):
    captured = {}

    def request(request, timeout):
        captured["url"] = request.full_url
        captured["authorization"] = request.get_header("Authorization")
        captured["body"] = json.loads(request.data)
        return Response({"data": [{"index": 1, "embedding": [3, 4]}, {"index": 0, "embedding": [1, 2]}]})

    gateway = configure()
    monkeypatch.setattr(models.urllib.request, "urlopen", request)

    encoded = models.encode(["first", "second"], gateway)

    assert captured["url"] == "https://gateway.example/v1/embeddings"
    assert captured["authorization"] == "Bearer secret"
    assert captured["body"] == {"model": "qwen3-embedding", "input": ["first", "second"]}
    assert [item.dense for item in encoded] == [[1.0, 2.0], [3.0, 4.0]]
    # The gateway gives dense vectors and nothing else; the lexical half of
    # retrieval is computed locally rather than asked of an API that has none.
    assert all(not hasattr(item, "sparse") for item in encoded)


def test_reranker_uses_system_gateway(monkeypatch):
    captured = {}

    def request(request, timeout):
        captured["url"] = request.full_url
        captured["body"] = json.loads(request.data)
        return Response({"results": [{"index": 1, "relevance_score": 0.9}, {"index": 0, "relevance_score": 0.2}]})

    gateway = configure()
    monkeypatch.setattr(models.urllib.request, "urlopen", request)

    assert models.rerank("question", ["first", "second"], gateway) == [0.2, 0.9]
    assert captured["url"] == "https://gateway.example/v1/rerank"
    assert captured["body"] == {
        "model": "qwen3-reranker",
        "query": "question",
        "documents": ["first", "second"],
        "top_n": 2,
    }
