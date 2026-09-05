"""Embedding spaces must never be mixed, even when dimensions are equal."""
from dataclasses import replace
from types import SimpleNamespace

import pytest
from qdrant_client import QdrantClient

from app import models, retrieve, store


@pytest.fixture
def index(monkeypatch):
    qdrant = QdrantClient(":memory:")
    monkeypatch.setattr(store, "client", lambda: qdrant)
    yield qdrant
    qdrant.close()


def gateway(model="a", scope="owner"):
    return models.GatewaySettings("https://gateway.invalid/v1", "dummy", model, "rerank", scope)


def write(profile, kb="one", dimension=2):
    collection = store.profile_collection(profile)
    store.upsert([dict(kb_id=kb, document_id="doc-"+kb, chunk_index=0, text="pump procedure", status="active")],
                 [models.Encoded([1.0] + [0.0] * (dimension-1))], collection=collection)
    return collection


def test_profile_identity_isolated_but_key_rotation_is_stable():
    original = gateway()
    profile = store.profile_collection(original)
    for changed in [replace(original, embedding_model="b"), replace(original, embedding_scope="other"),
                    replace(original, base_url="https://other.invalid/v1")]:
        assert store.profile_collection(changed) != profile
    assert store.profile_collection(replace(original, api_key="rotated", reranker_model="other")) == profile
    assert store.profile_collection(replace(original, base_url=original.base_url + "/")) == profile
    assert "dummy" not in profile and "gateway.invalid" not in profile


@pytest.mark.parametrize("dimension", [2, 3])
def test_same_and_different_dimensions_remain_separate(index, monkeypatch, dimension):
    a, b = gateway(), gateway("b")
    write(a, "one", 2)
    write(b, "two", dimension)
    monkeypatch.setattr(retrieve.ml, "encode", lambda texts, profile: [models.Encoded([1.0] + [0.0] * ((2 if profile == a else dimension)-1))])
    for profile, kb in [(a, "one"), (b, "two")]:
        result = retrieve.search("pump", [kb], gateway=profile, rerank_enabled=False)
        assert len(result) == 1 and result[0]["kb_id"] == kb
    with pytest.raises(store.ProfileNotIndexed):
        retrieve.search("pump", ["one"], gateway=b, rerank_enabled=False)


def test_missing_profile_never_falls_back_to_legacy(index, monkeypatch):
    store.upsert([dict(kb_id="one", document_id="doc", chunk_index=0, text="legacy", status="active")], [models.Encoded([1.0, 0.0])])
    def fail(*args, **kwargs):
        raise AssertionError("do not call embedding gateway for an unindexed profile")
    monkeypatch.setattr(retrieve.ml, "encode", fail)
    with pytest.raises(store.ProfileNotIndexed):
        retrieve.search("pump", ["one"], gateway=gateway())


def test_dimension_change_within_profile_preserves_old_vectors(index):
    profile = write(gateway())
    with pytest.raises(RuntimeError, match="profile dimension"):
        write(gateway(), dimension=3)
    assert store.inspect_document("doc-one", collection=profile)["indexed"]


def test_delete_removes_all_profiles_without_touching_other_collections(index):
    first, second = write(gateway()), write(gateway("b"))
    # Retained legacy/old profile copies must not resurrect a deleted document.
    unrelated = store.settings.collection + "__p1_not-a-profile"
    store.upsert([dict(kb_id="one", document_id="doc-one", chunk_index=0, text="other app", status="active")], [models.Encoded([1.0, 0.0])], collection=unrelated)
    store.delete_document("doc-one")
    assert not store.inspect_document("doc-one", collection=first)["indexed"]
    assert not store.inspect_document("doc-one", collection=second)["indexed"]
    assert store.inspect_document("doc-one", collection=unrelated)["indexed"]


def test_concurrent_profile_creation_validates_winner(index, monkeypatch):
    collection = write(gateway())
    original = index.collection_exists
    calls = 0
    def initially_absent(name):
        nonlocal calls
        calls += 1
        return False if calls == 1 else original(name)
    monkeypatch.setattr(index, "collection_exists", initially_absent)
    store.ensure_collection(index, 2, collection=collection)
    assert store.inspect_document("doc-one", collection=collection)["indexed"]


def test_reset_endpoint_cannot_delete_any_index(monkeypatch):
    from fastapi import HTTPException
    from app.main import reset_collection
    def fail():
        raise AssertionError("must not delete collection")
    monkeypatch.setattr(store, "reset_collection", fail)
    with pytest.raises(HTTPException) as error:
        reset_collection()
    assert error.value.status_code == 410
