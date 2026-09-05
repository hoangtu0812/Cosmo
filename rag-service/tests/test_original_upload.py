import base64

import pytest
from fastapi import HTTPException
from pydantic import ValidationError

from app import main, objects


def test_original_upload_stores_bytes_without_parsing_or_embedding(monkeypatch):
    stored = []
    monkeypatch.setattr(objects, 'put', lambda *args: stored.append(args))
    request = main.OriginalUpload(document_id='doc_12345678', content_base64=base64.b64encode(b'original').decode())
    assert main.store_original(request) == {'storage_key': 'knowledge-uploads/doc_12345678', 'size_bytes': 8}
    assert stored[0][1] == b'original'


def test_original_upload_rejects_bad_id_encoding_and_empty_content(monkeypatch):
    monkeypatch.setattr(objects, 'put', lambda *args: pytest.fail('invalid original reached storage'))
    with pytest.raises(ValidationError):
        main.OriginalUpload(document_id='../other', content_base64='YQ==')
    for content in ('', '%invalid%'):
        with pytest.raises(HTTPException):
            main.store_original(main.OriginalUpload(document_id='doc_12345678', content_base64=content))
