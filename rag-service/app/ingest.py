"""Document ingestion, implemented with LlamaIndex.

LlamaIndex owns parsing, node construction and metadata here — and nothing
else. It is never asked who may read a document; that decision has already
been made by the control plane before a document reaches this module.
"""

from __future__ import annotations

import logging
import os
import re
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterator

from llama_index.core import SimpleDirectoryReader
from llama_index.core.node_parser import MarkdownNodeParser, SentenceSplitter
from llama_index.core.schema import Document

from . import layout
from .config import settings

logger = logging.getLogger(__name__)

# Formats that carry their own heading hierarchy. Layout analysis returns
# Markdown, so an analysed scan joins them rather than being treated as prose.
STRUCTURED = {".md", ".markdown"}

# Formats worth sending to layout analysis: a scan and a table both arrive as
# a PDF, and neither survives a text-layer reader.
SCANNABLE = {".pdf"}

# Reading an unparseable file as text is only ever right for a file that is
# text. A .docx or .pptx is a zip container, and reading one as text indexes
# the archive's bytes as though they were the manual.
TEXT_FALLBACK = {
    ".txt", ".md", ".markdown", ".csv", ".json", ".log",
    ".xml", ".yaml", ".yml", ".htm", ".html",
}

# How hard to work at reading a PDF. `auto` escalates only a scan, `always`
# analyses every PDF — which is what a knowledge base of engineering tables
# needs, because a table has a perfectly good text layer and still comes back
# scrambled — and `off` never calls the service.
LAYOUT_MODES = {"auto", "always", "off"}

_OWN_HEADING = re.compile(r"^#{1,6}[ \t]+(.+)")


def _layout_mode(requested: str | None) -> str:
    """The mode this document asked for, falling back to the deployment's.

    An unrecognised value is ignored rather than refused: a knowledge base
    carrying a mode this build does not know about should still ingest.
    """
    for candidate in (requested, settings.layout_mode):
        value = (candidate or "").strip().lower()
        if value in LAYOUT_MODES:
            return value
    return "auto"


def _read(path: Path) -> list[Document]:
    try:
        return SimpleDirectoryReader(input_files=[str(path)]).load_data()
    except Exception as error:  # noqa: BLE001 - the format decides what happens next
        suffix = path.suffix.lower()
        if suffix not in TEXT_FALLBACK:
            # Failing here is the point: a binary format with no reader would
            # otherwise be indexed as mojibake and reported as ready.
            raise RuntimeError(f"Could not parse {suffix or 'the file'}: {error}") from error
        logger.warning("no reader matched %s, reading as plain text", path.name)
        return [Document(text=path.read_text(encoding="utf-8", errors="replace"))]


def _tag(documents: list[Document], filename: str) -> list[Document]:
    for document in documents:
        document.metadata = {**document.metadata, "file_name": filename}
    return documents


def _thin(documents: list[Document]) -> bool:
    """Whether the text layer is too sparse to be the document's real content.

    A scan read by a text-layer reader comes back empty or with a few stray
    characters per page. Measuring per page rather than in total keeps a long
    scan from looking substantial merely because it is long.
    """
    if not documents:
        return True
    total = sum(len(document.text.strip()) for document in documents)
    return total < settings.layout_min_chars_per_page * len(documents)


def _analysed(content: bytes, filename: str) -> list[Document]:
    """One Markdown document per page, as layout analysis returned it."""
    markdown = layout.analyze(content)
    documents: list[Document] = []
    carried = ""
    for label, text in layout.pages(markdown):
        body = text
        if carried and not body.lstrip().startswith("#"):
            # This page continues a section that began on an earlier one.
            # Restating the heading is what keeps its chunks filed under that
            # section instead of under whatever heading appears next.
            body = f"{carried}\n\n{body}"
        heading = layout.last_heading(text)
        if heading:
            carried = heading
        documents.append(Document(text=body, metadata={"file_name": filename, "page_label": label}))
    return documents


def _parse(path: Path, content: bytes, filename: str, mode: str) -> Iterator[dict]:
    """Read one file into documents, announcing any slow route before taking it.

    A generator, so the stage reaches the reader *before* the work it names
    rather than after it: layout analysis of a long scan is minutes during
    which a reader watching an unchanging stage cannot tell slow from stuck.
    Returns the documents and whether they are Markdown.
    """
    suffix = path.suffix.lower()

    if suffix in STRUCTURED:
        return _tag(_read(path), filename), True

    if suffix in SCANNABLE and mode != "off" and layout.configured():
        if mode == "always":
            yield {"stage": "layout", "message": "Analysing layout with Document Intelligence"}
            return _analysed(content, filename), True
        local = _tag(_read(path), filename)
        if not _thin(local):
            return local, False
        yield {"stage": "layout", "message": "No text layer found — analysing layout with Document Intelligence"}
        return _analysed(content, filename), True

    return _tag(_read(path), filename), False


def _nodes(documents: list[Document], structured: bool) -> list:
    splitter = SentenceSplitter(chunk_size=settings.chunk_size, chunk_overlap=settings.chunk_overlap)
    if not structured:
        return splitter.get_nodes_from_documents(documents)

    # A Markdown section is exactly as long as its author made it: a manual
    # under one heading becomes a single node of tens of thousands of
    # characters, which the embedding endpoint truncates without saying so.
    # Headings give the boundaries; the splitter gives the size.
    sections = MarkdownNodeParser().get_nodes_from_documents(documents)
    for node in sections:
        node.metadata["section"] = _heading_trail(node)
    return splitter(sections)


def parse(
    *,
    content: bytes,
    filename: str,
    kb_id: str,
    document_id: str,
    document_version: int,
    title: str,
    effective_date: str | None,
    layout_mode: str | None = None,
) -> Iterator[dict]:
    """Parse one document, yielding a stage as it starts and returning chunks.

    A generator rather than a plain call because reading a document is not one
    indivisible step: sending a scan to layout analysis is minutes of waiting
    that the caller has to be able to show while it happens. The chunk payloads
    come back as the generator's return value.

    The payload carries the metadata §14 asks for, so ranking and filtering can
    reason about authority and version rather than similarity alone.
    """
    suffix = Path(filename).suffix or ".txt"

    with tempfile.TemporaryDirectory() as workdir:
        path = Path(workdir) / f"document{suffix}"
        path.write_bytes(content)
        # The temporary file has to outlive the yields inside, which is why the
        # reader is driven from within the block rather than before it.
        documents, structured = yield from _parse(path, content, filename, _layout_mode(layout_mode))

    nodes = _nodes(documents, structured)

    ingested_at = datetime.now(timezone.utc).isoformat()
    chunks: list[dict] = []
    for index, node in enumerate(nodes):
        text = node.get_content().strip()
        if not text:
            continue
        chunks.append(
            {
                "kb_id": kb_id,
                "document_id": document_id,
                "document_version": document_version,
                "document_title": title,
                "source": filename,
                "section": _section_of(node),
                "page": node.metadata.get("page_label"),
                "chunk_index": index,
                "text": text,
                "status": "active",
                "effective_date": effective_date,
                "ingested_at": ingested_at,
            }
        )
    return chunks


def chunk(**arguments) -> list[dict]:
    """Parse a document when the caller has nothing to show progress on."""
    stages = parse(**arguments)
    while True:
        try:
            next(stages)
        except StopIteration as finished:
            return finished.value or []


def _heading_trail(node) -> str:
    """Name a section, ancestors first.

    MarkdownNodeParser records only the ancestors in `header_path` and leaves
    the node's own heading in the text, so a citation built from the metadata
    alone names the parent section and omits the one actually quoted.
    """
    trail = [part.strip() for part in str(node.metadata.get("header_path", "")).split("/") if part.strip()]
    own = _OWN_HEADING.match(node.get_content().lstrip())
    if own:
        heading = own.group(1).strip()
        if heading and (not trail or trail[-1] != heading):
            trail.append(heading)
    return " / ".join(trail)


def _section_of(node) -> str:
    """The heading trail recorded before splitting, if this format has one.

    It is stamped on the section node so every chunk split out of that section
    keeps it, including the ones that no longer begin with the heading.
    """
    return str(node.metadata.get("section") or "")


def storage_key(kb_id: str, document_id: str, filename: str) -> str:
    return f"{kb_id}/{document_id}{os.path.splitext(filename)[1]}"
