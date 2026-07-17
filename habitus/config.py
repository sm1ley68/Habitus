from pathlib import Path
from pydantic_settings import BaseSettings, SettingsConfigDict

class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    db_dsn: str = "postgresql://habitus:habitus@localhost:5544/habitus"
    city_region_code: int = 3
    poi_radius_m: int = 500
    embed_model: str = "BAAI/bge-m3"
    embed_dim: int = 1024
    embed_batch_size: int = 16
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
    rrf_k: int = 40  # сетка на golden-set: 40 стабильно ≥ 60/80 по recall/NDCG
    # 100, не 50: отфильтрованные пулы golden-сета 32–122, top_k=50 срезал
    # релевантных кандидатов ДО реранка (потолок recall 0.80 → 0.99 при 100).
    # Цена — реранкер видит до 100 доков (×2 латентность стадии rerank).
    retrieval_top_k: int = 100
    rerank_top_n: int = 10
    # макс. длина документа для кросс-энкодера реранкера (в токенах). 512 —
    # дефолт FlagReranker (полное качество, eval). На CPU без GPU реранк по
    # длинным докам минутный; урезание до 128 ускоряет ~3x при малой потере
    # (у объявлений ключевое — в начале текста). Тюнится через env RERANK_MAX_LENGTH.
    rerank_max_length: int = 512
    # proximity-rerank: доля структурного сигнала точной близости (walk_min_*)
    # в финальном score. 0.0 = чистая семантика реранкера, 1.0 = чистая близость.
    # Срабатывает только на осях, явно запрошенных пользователем (pq.geo).
    # Кривая на golden-set монотонна по весу (w→1 вырождается в ось разметки),
    # поэтому выбор — компромисс: 0.6 отдаёт явно запрошенной близости умеренное
    # большинство, семантика сохраняет 0.4. Сетка: scratchpad sweep 2026-07-17.
    proximity_weight: float = 0.6
    min_results: int = 5              # порог relaxation-петли
    relaxation_max_iters: int = 3
    langfuse_enabled: bool = False
    langfuse_host: str = ""
    langfuse_public_key: str = ""
    langfuse_secret_key: str = ""

settings = Settings()
