"""Document ingestion, implemented with LlamaIndex.

LlamaIndex owns parsing, node construction and metadata here — and nothing
else. It is never asked who may read a document; that decision has already
been made by the control plane before a document reaches this module.
"""

from __future__ import annotations

import logging
import os
import tempfile
from datetime import datetime, timezone
from pathlib import Path

from llama_index.core import SimpleDirectoryReader
from llama_index.core.node_parser import MarkdownNodeParser, SentenceSplitter
from llama_index.core.schema import Document

from .config import settings

logger = logging.getLogger(__name__)

# Extensions LlamaIndex's file readers cover; anything else is read as text so
# a stray extension still ingests rather than failing the whole document.
STRUCTURED = {".md", ".markdown"}


def _read(path: Path) -> list[Document]:
    try:
        return SimpleDirectoryReader(input_files=[str(path)]).load_data()
    except Exception:  # noqa: BLE001 - fall back rather than lose the document
        logger.warning("no reader matched %s, reading as plain text", path.name)
        text = path.read_text(encoding="utf-8", errors="replace")
        return [Document(text=text)]


def _splitter(suffix: str):
    """Split along document structure where the format exposes it.

    Markdown carries its own hierarchy, so headings become node boundaries and
    a chunk keeps the section it belongs to. Everything else falls back to
    sentence-aware splitting with overlap.
    """
    if suffix.lower() in STRUCTURED:
        return MarkdownNodeParser()
    return SentenceSplitter(
        chunk_size=settings.chunk_size,
        chunk_overlap=settings.chunk_overlap,
    )


def chunk(
    *,
    content: bytes,
    filename: str,
    kb_id: str,
    document_id: str,
    document_version: int,
    title: str,
    effective_date: str | None,
) -> list[dict]:
    """Parse one document into chunk payloads ready for the vector store.

    The payload carries the metadata §14 asks for, so ranking and filtering can
    reason about authority and version rather than similarity alone.
    """
    suffix = Path(filename).suffix or ".txt"

    with tempfile.TemporaryDirectory() as workdir:
        path = Path(workdir) / f"document{suffix}"
        path.write_bytes(content)
        documents = _read(path)

    for document in documents:
        document.metadata = {**document.metadata, "file_name": filename}

    nodes = _splitter(suffix).get_nodes_from_documents(documents)

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


def _section_of(node) -> str:
    """Recover the heading trail MarkdownNodeParser records, if any."""
    headers = [
        value.strip()
        for key, value in sorted(node.metadata.items())
        if key.lower().startswith("header") and isinstance(value, str) and value.strip()
    ]
    return " / ".join(headers)


def storage_key(kb_id: str, document_id: str, filename: str) -> str:
    return f"{kb_id}/{document_id}{os.path.splitext(filename)[1]}"
