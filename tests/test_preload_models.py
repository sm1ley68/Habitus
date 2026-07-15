import pytest

from habitus.online import preload_models


def test_preload_models_downloads_both_repositories(monkeypatch):
    seen = []
    monkeypatch.setattr(preload_models.settings, "embed_model", "example/embed")
    monkeypatch.setattr(preload_models.settings, "reranker_model", "example/reranker")
    monkeypatch.setattr(
        preload_models, "snapshot_download", lambda *, repo_id: seen.append(repo_id)
    )

    preload_models.preload_models()

    assert seen == ["example/embed", "example/reranker"]


def test_preload_models_propagates_download_failure(monkeypatch):
    def fail(*, repo_id):
        raise RuntimeError(f"cannot download {repo_id}")

    monkeypatch.setattr(preload_models, "snapshot_download", fail)

    with pytest.raises(RuntimeError, match="cannot download"):
        preload_models.preload_models()
