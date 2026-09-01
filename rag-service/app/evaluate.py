"""Measure retrieval against a curated set of questions.

Run as a command inside the knowledge service, because that is where the index
and the retrieval code already are:

    docker compose exec rag python -m app.evaluate --questions eval/questions.json

The point is comparison, not an absolute score. A recall of 0.74 means nothing
on its own; 0.74 against 0.61 from the same questions before a change is the
only evidence that the change helped. Write each run to a file with --report
and pass it as --baseline the next time.

The models must be the ones the index was built with. Measuring a collection
embedded by one model using the vectors of another produces numbers that look
like retrieval quality and are actually a dimension mismatch.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Sequence

from . import metrics, retrieve, store
from . import models as ml


@dataclass(frozen=True)
class Question:
    id: str
    query: str
    kb_ids: list[str]
    # Document id -> how relevant it is. Binary sets load as a gain of 1.
    gains: dict[str, float]

    @property
    def relevant(self) -> list[str]:
        return [document for document, gain in self.gains.items() if gain > 0]


@dataclass
class Outcome:
    question: Question
    retrieved: list[str]
    retrievers: list[str] = field(default_factory=list)
    recall: float = 0.0
    precision: float = 0.0
    reciprocal_rank: float = 0.0
    ndcg: float = 0.0
    error: str = ""


def load(path: Path) -> list[Question]:
    """Read a question set, refusing one that cannot be scored.

    A question with no expected documents scores zero however good retrieval
    is, and would quietly drag every average down. Rejecting it by name is
    better than reporting a number nobody can act on.
    """
    payload = json.loads(path.read_text(encoding="utf-8"))
    entries = payload.get("questions") if isinstance(payload, dict) else payload
    if not isinstance(entries, list) or not entries:
        raise ValueError(f"{path} contains no questions")

    questions: list[Question] = []
    for position, entry in enumerate(entries, start=1):
        identifier = str(entry.get("id") or f"q{position}")
        query = str(entry.get("query") or "").strip()
        if not query:
            raise ValueError(f"question {identifier} has no query")

        relevant = entry.get("relevant")
        if isinstance(relevant, dict):
            gains = {str(document): float(gain) for document, gain in relevant.items()}
        elif isinstance(relevant, list):
            gains = {str(document): 1.0 for document in relevant}
        else:
            gains = {}
        if not any(gain > 0 for gain in gains.values()):
            raise ValueError(f"question {identifier} names no relevant document")

        kb_ids = [str(kb) for kb in (entry.get("kb_ids") or []) if str(kb).strip()]
        if not kb_ids:
            raise ValueError(f"question {identifier} names no knowledge base to search")

        questions.append(Question(id=identifier, query=query, kb_ids=kb_ids, gains=gains))
    return questions


def measure(question: Question, k: int, gateway: ml.GatewaySettings | None = None) -> Outcome:
    """Ask one question and score what came back."""
    try:
        passages = retrieve.search(question.query, question.kb_ids, limit=k, gateway=gateway)
    except Exception as error:  # noqa: BLE001 - one bad question must not end the run
        return Outcome(question=question, retrieved=[], error=str(error))

    retrieved = metrics.unique([str(passage.get("document_id") or "") for passage in passages])
    retrievers = sorted({source for passage in passages for source in passage.get("matched", [])})
    return Outcome(
        question=question,
        retrieved=retrieved,
        retrievers=retrievers,
        recall=metrics.recall_at_k(retrieved, question.relevant, k),
        precision=metrics.precision_at_k(retrieved, question.relevant, k),
        reciprocal_rank=metrics.reciprocal_rank(retrieved, question.relevant),
        ndcg=metrics.ndcg_at_k(retrieved, question.gains, k),
    )


def summarise(outcomes: Sequence[Outcome], k: int) -> dict:
    scored = [outcome for outcome in outcomes if not outcome.error]

    def mean(name: str) -> float:
        if not scored:
            return 0.0
        return sum(getattr(outcome, name) for outcome in scored) / len(scored)

    return {
        "k": k,
        "questions": len(outcomes),
        "scored": len(scored),
        "failed": len(outcomes) - len(scored),
        "recall": round(mean("recall"), 4),
        "precision": round(mean("precision"), 4),
        "mrr": round(mean("reciprocal_rank"), 4),
        "ndcg": round(mean("ndcg"), 4),
        # How often the lexical half contributed anything. A hybrid index where
        # this stays at zero is a dense index with extra steps.
        "lexical_questions": sum(1 for outcome in scored if "sparse" in outcome.retrievers),
        "per_question": [
            {
                "id": outcome.question.id,
                "query": outcome.question.query,
                "recall": round(outcome.recall, 4),
                "ndcg": round(outcome.ndcg, 4),
                "reciprocal_rank": round(outcome.reciprocal_rank, 4),
                "retrieved": outcome.retrieved,
                "retrievers": outcome.retrievers,
                "error": outcome.error,
            }
            for outcome in outcomes
        ],
    }


METRIC_NAMES = ("recall", "precision", "mrr", "ndcg")


def render(report: dict, baseline: dict | None) -> str:
    lines = [
        f"{report['scored']}/{report['questions']} questions scored at k={report['k']}",
        "",
        f"{'metric':<12}{'value':>9}" + (f"{'baseline':>11}{'delta':>9}" if baseline else ""),
    ]
    for name in METRIC_NAMES:
        row = f"{name:<12}{report[name]:>9.4f}"
        if baseline:
            previous = float(baseline.get(name, 0.0))
            row += f"{previous:>11.4f}{report[name] - previous:>+9.4f}"
        lines.append(row)

    lines += [
        "",
        f"lexical retrieval contributed to {report['lexical_questions']}/{report['scored']} questions",
        "",
        f"{'question':<10}{'recall':>8}{'ndcg':>8}{'rr':>8}  query",
    ]
    for item in report["per_question"]:
        if item["error"]:
            lines.append(f"{item['id']:<10}{'failed':>24}  {item['error'][:60]}")
            continue
        lines.append(
            f"{item['id']:<10}{item['recall']:>8.2f}{item['ndcg']:>8.2f}"
            f"{item['reciprocal_rank']:>8.2f}  {item['query'][:56]}"
        )

    if report["failed"]:
        lines.append("")
        lines.append(f"{report['failed']} question(s) could not be asked; see the rows marked failed")
    return "\n".join(lines)


def _guard_dimensions(gateway: ml.GatewaySettings) -> None:
    """Refuse to score an index the configured model did not build.

    A mismatch here does not fail loudly on its own — it returns neighbours of
    a vector that means nothing, which reads as terrible retrieval rather than
    as a misconfiguration.
    """
    try:
        probe = ml.encode(["kiểm tra cấu hình"], gateway)[0]
    except Exception as error:  # noqa: BLE001 - a traceback helps nobody here
        raise SystemExit(f"could not reach the model gateway: {error}") from error
    qdrant = store.client()
    if not qdrant.collection_exists(store.settings.collection):
        raise SystemExit(f"collection {store.settings.collection} does not exist; ingest documents first")
    vectors = qdrant.get_collection(store.settings.collection).config.params.vectors
    indexed = getattr(vectors.get(store.DENSE) if isinstance(vectors, dict) else None, "size", None)
    if indexed != len(probe.dense):
        raise SystemExit(
            f"the configured embedding model produces {len(probe.dense)} dimensions but the index holds "
            f"{indexed}; measure with the model the index was built with, or re-index first"
        )


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Measure knowledge retrieval against curated questions.")
    parser.add_argument("--questions", default="eval/questions.json", help="path to the question set")
    parser.add_argument("--k", type=int, default=10, help="how many results a question is scored over")
    parser.add_argument("--report", help="write the run to this file, to use as a later baseline")
    parser.add_argument("--baseline", help="a previous report to compare this run against")
    parser.add_argument("--gateway-base-url", default=os.environ.get("GATEWAY_BASE_URL", ""))
    parser.add_argument("--gateway-api-key", default=os.environ.get("GATEWAY_API_KEY", ""))
    parser.add_argument("--embedding-model", default=os.environ.get("EMBEDDING_MODEL", ""))
    parser.add_argument("--reranker-model", default=os.environ.get("RERANKER_MODEL", ""))
    arguments = parser.parse_args(argv)

    if not arguments.gateway_base_url or not arguments.embedding_model or not arguments.reranker_model:
        raise SystemExit(
            "the gateway base url, embedding model and reranker model are required; "
            "pass them as flags or set GATEWAY_BASE_URL, EMBEDDING_MODEL and RERANKER_MODEL"
        )

    gateway = ml.gateway_settings(
        arguments.embedding_model,
        arguments.reranker_model,
        arguments.gateway_base_url,
        arguments.gateway_api_key,
    )
    _guard_dimensions(gateway)

    questions = load(Path(arguments.questions))
    report = summarise([measure(question, arguments.k, gateway) for question in questions], arguments.k)

    baseline = None
    if arguments.baseline:
        baseline = json.loads(Path(arguments.baseline).read_text(encoding="utf-8"))
    print(render(report, baseline))

    if arguments.report:
        Path(arguments.report).write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"\nreport written to {arguments.report}")
    return 1 if report["failed"] else 0


if __name__ == "__main__":
    sys.exit(main())
