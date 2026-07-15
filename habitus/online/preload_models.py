"""Download the online search models into the configured Hugging Face cache."""

from huggingface_hub import snapshot_download

from habitus.config import settings


def preload_models() -> None:
    """Ensure both lazily loaded model repositories are present locally."""
    snapshot_download(repo_id=settings.embed_model)
    snapshot_download(repo_id=settings.reranker_model)


if __name__ == "__main__":
    preload_models()
