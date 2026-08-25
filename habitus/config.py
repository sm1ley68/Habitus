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
    # На латентность реранка больше не влияет: до кросс-энкодера доезжает
    # rerank_pool_n, а не весь top_k (см. prefilter_pool). Цена большого
    # значения — только время самого retrieval и памяти под кандидатов.
    retrieval_top_k: int = 100
    rerank_top_n: int = 10
    # Сколько объектов возвращает /search сверх первой страницы: шлюз сохраняет
    # весь набор и отдаёт «показать ещё» из своей таблицы, не гоняя поиск заново.
    result_max_n: int = 30
    # Сколько кандидатов доезжает до кросс-энкодера. Реранк линеен по числу пар и
    # составляет львиную долю латентности поиска; retrieval_top_k=100 отбирался под
    # recall ДО реранка, а не под то, сколько пар кросс-энкодер обязан посмотреть.
    # Пул собирается из двух голов (RRF и proximity), чтобы срез не выкидывал
    # ни семантически близкие, ни структурно близкие объекты. Тюнится через
    # env RERANK_POOL_N.
    # Замер 100/60/40/25 — docs/notes/rerank-pool-2026-08-18.md: качество плоское
    # по всему диапазону (recall@10 0.33–0.35 при критерии «не более 0.03
    # просадки»), время линейно по pool_n. Замер промежуточный — сделан до того,
    # как T5 сняла фильтр ориентации, и до перевода метрик на precision@10; по
    # нему 60 выглядит лучшей точкой, 40 оставлено как более консервативное.
    # Результат ≤ result_max_n: при pool_n меньше 30 выдача /search ужмётся до
    # размера пула, и «показать ещё» получит меньше объектов, чем обещает
    # result_max_n.
    rerank_pool_n: int = 40
    # Потолок пула на машине без CUDA — там кросс-энкодер идёт на CPU и линеен
    # по числу пар. Замер docs/notes/rerank-pool-2026-08-18.md: на 25 recall@10
    # держится (0.34 против 0.34 у сотни), проседает только MRR (0.55 → 0.48).
    # Это осознанный размен: голова выдачи чуть хуже упорядочена, зато шлюз
    # успевает дождаться ответа и не показывает «ошибка ИИ» на живом поиске.
    rerank_pool_n_cpu: int = 25
    # Макс. длина пары для кросс-энкодера реранкера (в токенах).
    # Замерено на golden-set при retrieval_top_k=100 (реранк 50 пар на CPU):
    #   512 — recall@10 0.68, NDCG 0.65, 52.3 с
    #   256 — recall@10 0.65, NDCG 0.66, 23.3 с   ← выбрано
    #   192 —              не мерено,             17.3 с
    # Урезание длины выгоднее урезания пула: k=50 при 512 даёт recall 0.59, то
    # есть втрое дороже по качеству за тот же выигрыш во времени. Работает это
    # потому, что build_doc_text ставит структурные факты перед описанием — они
    # занимают максимум 85 токенов и в окно попадают целиком, режется только проза.
    # Тюнится через env RERANK_MAX_LENGTH.
    rerank_max_length: int = 256
    # proximity-rerank: доля структурного сигнала точной близости (walk_min_*)
    # в финальном score. 0.0 = чистая семантика реранкера, 1.0 = чистая близость.
    # Срабатывает только на осях, явно запрошенных пользователем (pq.geo).
    # Кривая на golden-set монотонна по весу (w→1 вырождается в ось разметки),
    # поэтому выбор — компромисс: 0.6 отдаёт явно запрошенной близости умеренное
    # большинство, семантика сохраняет 0.4. Сетка: scratchpad sweep 2026-07-17.
    proximity_weight: float = 0.6
    # Вес совпадения ориентации окон в финальном бленде. Не фильтр: данные об
    # ориентации есть у ~2% объявлений (извлекаются из прозы описания, см.
    # habitus/clean/windows.py), поэтому жёсткое условие отсекало базу целиком
    # и подменялось relaxation-ом на каждом запросе.
    # Достаёт только тех, кто попал в пул prefilter_pool: объявление с нужными
    # окнами на 50-й позиции RRF бонуса не получит — головы по ориентации в
    # пуле нет. При покрытии ~2% это приемлемо, но объясняет, почему сигнал
    # иногда «не срабатывает» на глаз.
    orientation_weight: float = 0.15
    min_results: int = 5              # порог relaxation-петли
    relaxation_max_iters: int = 3
    langfuse_enabled: bool = False
    langfuse_host: str = ""
    langfuse_public_key: str = ""
    langfuse_secret_key: str = ""

settings = Settings()
