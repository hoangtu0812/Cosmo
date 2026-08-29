"""Azure AI Document Intelligence layout analysis.

pypdf can only read a text layer. It returns nothing at all for a scan, and it
flattens a table into whatever order the glyphs happen to sit in, which turns a
specification table into a run of unrelated numbers. Documents that arrive as
scans, or that carry the engineering tables the answers actually depend on, are
therefore analysed by the `prebuilt-layout` model, which returns Markdown with
the table structure intact — the same Markdown the ingestion path already knows
how to split along headings.

Analysis is billed per page, so it is not the default route. `auto` escalates
only when the local text layer comes back too thin to be real text, `always` is
for a knowledge base known to be scans, and `off` never calls the service.
"""

from __future__ import annotations

import base64
import json
import logging
import re
import time
import urllib.error
import urllib.request

from .config import settings

logger = logging.getLogger(__name__)

# Document Intelligence marks up the furniture it recognises as HTML comments.
# Page headers, footers and page numbers repeat on every page, so leaving them
# in would put the same company name into several hundred chunks.
_PAGE_BREAK = re.compile(r"<!--\s*PageBreak\s*-->", re.IGNORECASE)
_PAGE_NUMBER = re.compile(r"<!--\s*PageNumber=\"(.*?)\"\s*-->", re.IGNORECASE | re.DOTALL)
_FURNITURE = re.compile(r"<!--\s*Page(?:Header|Footer|Number)=\".*?\"\s*-->", re.IGNORECASE | re.DOTALL)
_HEADING = re.compile(r"^#{1,6}[ \t]+\S.*$", re.MULTILINE)


def configured() -> bool:
    """Whether a service is reachable at all.

    Whether it should be used for a given document is a separate question, and
    one the owning knowledge base answers rather than the deployment.
    """
    return bool(settings.layout_endpoint)


def _request(url: str, data: bytes | None) -> tuple[dict, dict[str, str]]:
    request = urllib.request.Request(
        url,
        data=data,
        headers={
            "Ocp-Apim-Subscription-Key": settings.layout_key,
            **({"Content-Type": "application/json"} if data else {}),
        },
        method="POST" if data else "GET",
    )
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            body = response.read()
            headers = {key.lower(): value for key, value in response.headers.items()}
    except urllib.error.HTTPError as error:
        detail = error.read(2048).decode("utf-8", errors="replace").strip()
        raise RuntimeError(f"Document Intelligence returned {error.code}: {detail}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"Document Intelligence is unreachable: {error.reason}") from error

    if not body:
        return {}, headers
    try:
        decoded = json.loads(body)
    except json.JSONDecodeError as error:
        raise RuntimeError("Document Intelligence returned invalid JSON") from error
    return (decoded if isinstance(decoded, dict) else {}), headers


def analyze(content: bytes) -> str:
    """Return the document as Markdown, waiting for the analysis to finish.

    The service is asynchronous: the first call queues the work and answers with
    a status URL. A two hundred page scan takes minutes, which is why the
    deadline here is measured in minutes rather than seconds — the caller is an
    ingestion job reporting progress, not a request a browser is waiting on.
    """
    endpoint = settings.layout_endpoint
    url = (
        f"{endpoint}/documentintelligence/documentModels/prebuilt-layout:analyze"
        f"?api-version={settings.layout_api_version}&outputContentFormat=markdown"
    )
    payload = json.dumps({"base64Source": base64.b64encode(content).decode("ascii")}).encode("utf-8")
    _, headers = _request(url, payload)

    operation = headers.get("operation-location")
    if not operation:
        raise RuntimeError("Document Intelligence did not return an operation location")

    deadline = time.time() + settings.layout_timeout
    while True:
        result, _ = _request(operation, None)
        status = str(result.get("status", "")).lower()
        if status == "succeeded":
            markdown = str((result.get("analyzeResult") or {}).get("content") or "")
            if not markdown.strip():
                raise RuntimeError("Document Intelligence found no text in the document")
            return markdown
        if status in {"failed", "canceled"}:
            error = (result.get("error") or {}).get("message") or status
            raise RuntimeError(f"Document Intelligence analysis {status}: {error}")
        if time.time() >= deadline:
            raise RuntimeError("Document Intelligence analysis did not finish in time")
        time.sleep(settings.layout_poll_interval)


def pages(markdown: str) -> list[tuple[str, str]]:
    """Split analysed Markdown into pages, labelled the way the document is.

    Citations are only useful if they name the page a reader can turn to, so the
    printed page number is preferred over the position in the file whenever the
    service recognised one.
    """
    result: list[tuple[str, str]] = []
    for position, raw in enumerate(_PAGE_BREAK.split(markdown), start=1):
        printed = _PAGE_NUMBER.search(raw)
        label = printed.group(1).strip() if printed else str(position)
        text = _FURNITURE.sub("", raw).strip()
        if text:
            result.append((label or str(position), text))
    return result


def last_heading(text: str) -> str:
    """The final heading line on a page, to carry onto the next one.

    A section that runs across a page break leaves the following page with no
    heading of its own, and its chunks would then be filed under whatever came
    before it in the document.
    """
    headings = _HEADING.findall(text)
    return headings[-1].strip() if headings else ""
