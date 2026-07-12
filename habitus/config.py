from pathlib import Path
from pydantic_settings import BaseSettings, SettingsConfigDict

class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    db_dsn: str = "postgresql://habitus:habitus@localhost:5544/habitus"
    city_region_code: int = 3
    poi_radius_m: int = 500
    embed_model: str = "BAAI/bge-m3"
    embed_dim: int = 1024
    data_dir: Path = Path("./data")
    kaggle_username: str = ""
    kaggle_key: str = ""

    # --- online-фаза ---
    openrouter_api_key: str = ""
    llm_model: str = "qwen/qwen-2.5-72b-instruct"
    llm_fallbacks: list[str] = ["deepseek/deepseek-chat", "openai/gpt-4o-mini"]
    llm_base_url: str = "https://openrouter.ai/api/v1"
    llm_timeout_s: float = 30.0
    reranker_model: str = "BAAI/bge-reranker-v2-m3"
    ors_base_url: str = "https://api.openrouteservice.org"
    ors_api_key: str = ""
    rrf_k: int = 60
    retrieval_top_k: int = 50
    rerank_top_n: int = 10
    min_results: int = 5              # порог relaxation-петли
    relaxation_max_iters: int = 3
    langfuse_enabled: bool = False
    langfuse_host: str = ""
    langfuse_public_key: str = ""
    langfuse_secret_key: str = ""

settings = Settings()
