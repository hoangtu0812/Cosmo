"""What comes out of parsing decides everything downstream.

A chunk too large to embed is silently truncated, a chunk filed under the wrong
heading is cited wrongly, and a binary read as text is indexed as gibberish and
reported as ready. These tests hold those three outcomes shut.
"""

import io
import sys
import zipfile
from dataclasses import replace
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from llama_index.core.schema import Document  # noqa: E402

from app import ingest, layout  # noqa: E402


def _explode(content):
    raise AssertionError("layout analysis must not run for this document")


def read(path: Path, content: bytes, filename: str, mode: str):
    """Drive the parse generator to completion and return what it read."""
    stages = ingest._parse(path, content, filename, mode)
    announced = []
    while True:
        try:
            announced.append(next(stages))
        except StopIteration as finished:
            documents, structured = finished.value
            return documents, structured, announced


def parse(content: bytes, filename: str) -> list[dict]:
    return ingest.chunk(
        content=content,
        filename=filename,
        kb_id="kb_1",
        document_id="doc_1",
        document_version=1,
        title="Document",
        effective_date=None,
    )


class TestChunkSize:
    def test_a_long_markdown_section_is_split(self):
        # One heading over a long body is a single MarkdownNodeParser node. Left
        # whole it exceeds any embedding context and is truncated upstream.
        body = "# Quy trinh ATSK\n\n" + ("Noi dung dai dong. " * 4000)
        chunks = parse(body.encode("utf-8"), "qt.md")
        assert len(chunks) > 1
        assert max(len(chunk["text"]) for chunk in chunks) < 8000

    def test_plain_text_is_still_split(self):
        chunks = parse(("Cau van binh thuong. " * 3000).encode("utf-8"), "note.txt")
        assert len(chunks) > 1


class TestSection:
    def test_names_the_section_that_was_quoted(self):
        body = "# Quy trinh\n\nMo dau.\n\n## Buoc 2\n\nSiet bu-long 40 Nm.\n"
        sections = {chunk["section"] for chunk in parse(body.encode("utf-8"), "qt.md")}
        assert "Quy trinh / Buoc 2" in sections

    def test_split_pieces_keep_the_section_of_their_heading(self):
        body = "# Quy trinh\n\n## Buoc 2\n\n" + ("Chi tiet thao tac. " * 3000)
        chunks = parse(body.encode("utf-8"), "qt.md")
        pieces = [chunk for chunk in chunks if chunk["section"].endswith("Buoc 2")]
        assert len(pieces) > 1, "every piece of a section must keep that section"


class TestUnreadableFormats:
    def test_a_binary_without_a_reader_is_refused(self):
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w") as archive:
            archive.writestr("ppt/slides/slide1.xml", "<p:sld>Bao cao</p:sld>")
        with pytest.raises(RuntimeError):
            parse(buffer.getvalue(), "deck.pptx")

    def test_a_text_format_without_a_reader_still_ingests(self):
        chunks = parse(b"<html><body>Ap suat van hanh 12 bar.</body></html>", "page.html")
        assert chunks and "12 bar" in chunks[0]["text"]


ANALYSED = (
    '<!-- PageHeader="Cong ty ABC" -->\n\n# Quy trinh bao duong\n\nBuoc 1: khoa van.\n\n'
    '<!-- PageNumber="7" -->\n\n<!-- PageBreak -->\n\n'
    'Buoc 2: siet bu-long 40 Nm.\n\n<!-- PageFooter="Ban hanh 2024" -->\n\n<!-- PageNumber="8" -->'
)


class TestLayoutMarkdown:
    def test_pages_are_split_and_labelled_as_printed(self):
        pages = layout.pages(ANALYSED)
        assert [label for label, _ in pages] == ["7", "8"]

    def test_repeated_page_furniture_is_removed(self):
        joined = "\n".join(text for _, text in layout.pages(ANALYSED))
        assert "Cong ty ABC" not in joined
        assert "Ban hanh 2024" not in joined

    def test_a_section_running_past_a_page_break_keeps_its_heading(self, monkeypatch):
        monkeypatch.setattr(layout, "analyze", lambda content: ANALYSED)
        documents = ingest._analysed(b"pdf", "qt.pdf")
        assert documents[1].text.lstrip().startswith("# Quy trinh bao duong")
        assert documents[1].metadata["page_label"] == "8"


class TestLayoutRouting:
    """Which documents are analysed is the owning knowledge base's choice.

    Analysis is billed per page, so a base of typed memos must be able to stay
    on the local reader while a base of scanned drawings sends everything.
    """

    @pytest.fixture
    def analysed(self, monkeypatch):
        monkeypatch.setattr(layout, "configured", lambda: True)
        monkeypatch.setattr(layout, "analyze", lambda content: ANALYSED)

    def readable(self, monkeypatch):
        monkeypatch.setattr(ingest, "_read", lambda path: [Document(text="Noi dung day du. " * 40)])

    def test_auto_escalates_a_pdf_with_no_usable_text_layer(self, monkeypatch, analysed):
        monkeypatch.setattr(ingest, "_read", lambda path: [])
        documents, structured, _ = read(Path("document.pdf"), b"pdf", "qt.pdf", "auto")
        assert structured is True
        assert "Quy trinh bao duong" in documents[0].text

    def test_auto_leaves_a_readable_pdf_on_the_local_reader(self, monkeypatch, analysed):
        self.readable(monkeypatch)
        documents, structured, _ = read(Path("document.pdf"), b"pdf", "qt.pdf", "auto")
        assert structured is False
        assert "Noi dung day du" in documents[0].text

    def test_the_slow_route_is_announced_before_it_is_taken(self, monkeypatch, analysed):
        # The stage has to arrive before the minutes of waiting it names, not
        # after them, or the reader cannot tell slow from stuck.
        order = []
        monkeypatch.setattr(ingest, "_read", lambda path: [])
        monkeypatch.setattr(layout, "analyze", lambda content: order.append("analysed") or ANALYSED)
        stages = ingest._parse(Path("document.pdf"), b"pdf", "qt.pdf", "auto")
        first = next(stages)
        assert first["stage"] == "layout"
        assert order == [], "the stage must be reported before the analysis runs"

    def test_always_analyses_a_readable_pdf_too(self, monkeypatch, analysed):
        # The case the whole setting exists for: a table has a perfectly good
        # text layer and still comes back scrambled.
        self.readable(monkeypatch)
        _, structured, _ = read(Path("document.pdf"), b"pdf", "qt.pdf", "always")
        assert structured is True

    def test_off_never_analyses_even_a_scan(self, monkeypatch, analysed):
        monkeypatch.setattr(layout, "analyze", _explode)
        monkeypatch.setattr(ingest, "_read", lambda path: [Document(text="")])
        _, structured, _ = read(Path("document.pdf"), b"pdf", "qt.pdf", "off")
        assert structured is False

    def test_an_unconfigured_service_is_never_called(self, monkeypatch):
        monkeypatch.setattr(layout, "configured", lambda: False)
        monkeypatch.setattr(layout, "analyze", _explode)
        monkeypatch.setattr(ingest, "_read", lambda path: [Document(text="")])
        _, structured, _ = read(Path("document.pdf"), b"pdf", "qt.pdf", "always")
        assert structured is False


class TestLayoutModeResolution:
    def test_the_knowledge_base_wins_over_the_deployment(self, monkeypatch):
        monkeypatch.setattr(ingest, "settings", replace(ingest.settings, layout_mode="auto"))
        assert ingest._layout_mode("always") == "always"

    def test_an_unset_mode_falls_back_to_the_deployment(self, monkeypatch):
        monkeypatch.setattr(ingest, "settings", replace(ingest.settings, layout_mode="always"))
        assert ingest._layout_mode(None) == "always"
        assert ingest._layout_mode("") == "always"

    def test_an_unknown_mode_is_ignored_rather_than_refused(self, monkeypatch):
        monkeypatch.setattr(ingest, "settings", replace(ingest.settings, layout_mode="auto"))
        assert ingest._layout_mode("aggressive") == "auto"

    def test_a_broken_deployment_value_still_resolves(self, monkeypatch):
        monkeypatch.setattr(ingest, "settings", replace(ingest.settings, layout_mode="nonsense"))
        assert ingest._layout_mode(None) == "auto"
