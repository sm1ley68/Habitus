# План: доведение Habitus до сильного MVP (бек + ML) — 18 августа 2026

Источник: разбор кода и данных 18 августа. Шесть направлений — скорость,
многоходовость чата, честность данных, измеримость качества, эксплуатация,
мелкие продуктовые дыры. Фронтенд не трогаем.

Решения владельца, принятые до старта:

- **Ориентация окон** — мягкий сигнал, не жёсткий фильтр; покрытие показываем честно.
- **Сборщик Циана** — только щадящий режим объёма; код парсера не трогаем.
- **Golden-set** — расширяем правилами + текстовыми запросами; ручная разметка не требуется.

## Контекст

- `habitus/` — Python/FastAPI ML-сервис (порт 8000). Источник правды по формам
  данных — `habitus/online/schema.py`.
- `backend/` — Go/Fiber шлюз (порт 8080), публичный API `/api/v1`.
- БД — Postgres 16 + PostGIS + pgvector на порту 5544. Таблица `listings`
  принадлежит Python-части, Go в неё **только читает**.
- Задачи из `habitus-tz.pdf` (честный `/health`, фильтры `/geo/listings`,
  эндпоинт статистики) выполняет другой разработчик — **в этом плане их нет**,
  и файлы `internal/http/handlers/health_handler.go`,
  `internal/repository/listing_repo.go` (метод `ListInBBox`),
  `internal/service/geo_layers_service.go` мы не трогаем, чтобы не устроить
  конфликт в `main`.

## Global Constraints

1. **Работа напрямую в `main`.** Отдельных веток не заводим. Один коммит на
   задачу, сообщение по-русски в формате Conventional Commits
   (`feat:`/`fix:`/`chore:`/`refactor:`/`test:`/`eval:`/`docs:`).
   **Никаких трейлеров и подписей** — ни `Co-Authored-By`, ни упоминаний Claude.
2. **Не выдумывать факты о городе.** Отсутствующее значение остаётся `NULL` и
   деградирует до отсутствия блока. Синтетический ноль вместо замера запрещён.
3. **Координаты везде `[lng, lat]`, WGS84 (EPSG:4326).** Порядок не менять.
4. **Изменения контракта только аддитивные.** Новые поля в
   `habitus/online/schema.py` и Go-DTO допустимы; существующие поля нельзя
   переименовывать, удалять или менять по смыслу — фронтенд вне нашей области
   и не должен сломаться.
5. **Тесты обязательны в каждой задаче**, не «если успеем». Python —
   `uv run pytest`, Go — `cd backend && go test ./...`. Тесты Go не ходят
   в реальную БД и сеть (подставные объекты). Python-тесты с БД сами скипаются,
   если Postgres не поднят.
6. **Не трогать**: `backend/internal/cian/**` (код сборщика — неаккуратный
   эксперимент лишает доступа к источнику), `frontend/**`, `.env`.
   Файлы из раздела «Контекст» выше, занятые другим разработчиком, тоже не трогаем.
7. **Секреты не коммитить.** Если `.env` появился в `git status` — остановиться.
8. Комментарии по-русски и только там, где объясняют «почему», а не «что».
9. Параметры в SQL — только через плейсхолдеры (`%s` в psycopg, `$1` в pgx).
   Склейка строк запроса запрещена.

---

## Task 1 — Реранк только по префильтрованному пулу + раздельные локи инференса

**Зачем.** `run_search` отдаёт кросс-энкодеру весь пул `retrieval_top_k=100`
кандидатов, и только потом `proximity_rerank` берёт 10. Реранк 50 пар на CPU —
23.3 с (`docs/notes/eval-baseline-2026-08-14.md`), то есть реранк и есть
латентность поиска. Плюс `INFERENCE_LOCK` — один общий лок на эмбеддер и
реранкер, из-за чего кодирование запроса второго пользователя ждёт реранк
первого.

**Что сделать.**

1. В `habitus/config.py` добавить параметр:
   ```python
   # Сколько кандидатов доезжает до кросс-энкодера. Реранк линеен по числу пар и
   # составляет львиную долю латентности поиска; retrieval_top_k=100 отбирался под
   # recall ДО реранка, а не под то, сколько пар кросс-энкодер обязан посмотреть.
   # Пул собирается из двух голов (RRF и proximity), чтобы срез не выкидывал
   # ни семантически близкие, ни структурно близкие объекты. Тюнится через
   # env RERANK_POOL_N.
   rerank_pool_n: int = 40
   ```
2. В `habitus/online/rerank.py` добавить функцию:
   ```python
   def prefilter_pool(pq: ParsedQuery, candidates: list[Candidate],
                      pool_n: int | None = None) -> list[Candidate]:
   ```
   Правила, строго:
   - `pool_n` по умолчанию — `settings.rerank_pool_n`.
   - `len(candidates) <= pool_n` → вернуть `candidates` как есть (тот же порядок,
     те же объекты).
   - `pq.geo` пуст → вернуть `candidates[:pool_n]` (порядок RRF сохраняется).
   - `pq.geo` задан → пул = объединение двух голов, каждая длиной
     `ceil(pool_n / 2)`:
     - **RRF-голова** — первые `ceil(pool_n/2)` элементов входного порядка;
     - **proximity-голова** — кандидаты, отсортированные по возрастанию
       `_proximity_raw(pq, c)` (тай-брейк по `external_id`); кандидаты, у которых
       `_proximity_raw` вернул `None`, в эту голову не попадают вовсе.
     Объединение по `external_id` без дублей; итоговый порядок — сначала элементы
     RRF-головы в их исходном порядке, затем добавленные proximity-головой в её
     порядке. Итог обрезать до `pool_n`.
   - Функция чистая: не мутирует вход, детерминирована при равных скорах.
3. В `habitus/online/pipeline.py` в шаге 4 прогонять `prefilter_pool` **до**
   `rerank`: `pool = prefilter_pool(pq, cands)` → `rerank(query, pool, top_n=len(pool))`
   → `proximity_rerank(...)`. При деградации реранкера (`except`) дальше идёт
   `pool`, а не полный `cands`.
4. **Тот же срез обязан быть в eval**, иначе метрика меряет не то, что
   отгружается. В `habitus/eval/runner.py` варианты `rrf+rerank` и
   `rrf+rerank+prox` должны звать `prefilter_pool` ровно так же, как pipeline.
   Вариант `rrf+prox` (без реранка) остаётся на полном пуле.
5. Раздельные локи в `habitus/embed/encode.py`: вместо одного
   `INFERENCE_LOCK` завести `EMBED_LOCK` и `RERANK_LOCK` (оба `threading.Lock`).
   `encode_texts` берёт `EMBED_LOCK`, `habitus/online/rerank.py::rerank` — 
   `RERANK_LOCK`. Причина в комментарии: непотокобезопасны сами fast-токенизаторы,
   а они у моделей разные, поэтому один общий лок сериализовал больше, чем нужно.
   Имя `INFERENCE_LOCK` сохранить как алиас `EMBED_LOCK` — на него ссылаются
   существующие импорты; удалять публичное имя в этой задаче нельзя.
6. Тайминги стадий наружу: в `SearchResponse` добавить поле
   `timings: dict[str, float] = {}` — миллисекунды по стадиям
   (`parse`, `encode`, `resolve_area`, `retrieval`, `rerank`, `explain`).
   Собирать из уже существующего `habitus/online/trace.py::span` — добавить туда
   опциональный сборщик (например, контекст-менеджер `collector()` или параметр
   `sink`), не ломая текущие вызовы. Отсутствующая стадия в словарь не попадает
   (нулей не выдумываем).

**Файлы.** `habitus/config.py`, `habitus/online/rerank.py`,
`habitus/online/pipeline.py`, `habitus/online/schema.py`,
`habitus/online/trace.py`, `habitus/embed/encode.py`, `habitus/eval/runner.py`,
`tests/test_rerank.py`, `tests/test_pipeline.py`.

**Тесты (обязательный минимум).**
- `prefilter_pool` без `pq.geo` — срез по RRF-порядку, длина `pool_n`.
- `prefilter_pool` с `pq.geo` — в пул попал структурно близкий кандидат, стоявший
  в хвосте RRF-порядка (за пределами `pool_n`), и при этом голова RRF сохранена.
- Кандидаты без `walk_min_*` не ломают функцию и не вытесняют тех, у кого данные есть.
- `len(candidates) <= pool_n` — вход возвращается неизменным.
- Детерминизм: два вызова на одном входе дают одинаковый порядок.
- `run_search` отдаёт `timings` с ключом `retrieval` (на фейковых зависимостях).

**Приёмка.**
1. `uv run pytest` зелёный, тестов стало больше.
2. `rerank` в pipeline получает не больше `settings.rerank_pool_n` пар (проверить
   тестом на подставном реранкере, считающем размер входа).
3. `git show --stat HEAD` — только файлы из списка.

---

## Task 2 — Замер: сколько стоит срез пула и какая конфигурация реранка остаётся

**Зачем.** Task 1 меняет качество ранжирования. Без замера это вера, а не
инженерия. Инфраструктура абляций уже есть — `uv run habitus eval`.

**Что сделать.**

1. Прогнать `uv run habitus eval` на текущем golden-set (`habitus/eval/queries.yaml`)
   для конфигураций, задавая их через env:
   - `RERANK_POOL_N=100` (то есть фактически «как было»), `RERANK_MAX_LENGTH=256`
   - `RERANK_POOL_N=60`, `RERANK_MAX_LENGTH=256`
   - `RERANK_POOL_N=40`, `RERANK_MAX_LENGTH=256`
   - `RERANK_POOL_N=25`, `RERANK_MAX_LENGTH=256`
   Для каждой — recall@10, NDCG@10, MRR по варианту `rrf+rerank+prox` и
   **время прогона стадии реранка** (замерять `time` всего прогона eval
   достаточно, если фиксировать, что остальные стадии в вариантах одинаковы).
2. Ключ `OPENROUTER_API_KEY` для этого не нужен: parse-accuracy без ключа
   пропускается, retrieval-часть работает от `expected_parse`. Если ключ в `.env`
   есть — не выводить его никуда в отчёт.
3. Записать `docs/notes/rerank-pool-2026-08-18.md`: таблица
   «pool_n | recall@10 | NDCG@10 | MRR | время», вывод, и **честная оговорка**
   о том, чего этот golden-set не меряет (все запросы структурные — см.
   `docs/notes/eval-baseline-2026-08-14.md`).
4. Зафиксировать выбранный дефолт `rerank_pool_n` в `habitus/config.py`
   с комментарием, ссылающимся на замер (как это сделано для `rerank_max_length`).
   Критерий выбора: наименьший `pool_n`, теряющий не более 0.03 recall@10
   относительно `pool_n=100`.
5. БД должна быть поднята (`docker compose up db`, порт 5544, уже работает).
   Модели берутся из локального HF-кэша; сети на скачивание может не быть —
   если модель не находится, это статус BLOCKED, а не повод выдумать цифры.

**Файлы.** `docs/notes/rerank-pool-2026-08-18.md`, `habitus/config.py`.

**Приёмка.**
1. В заметке — реальные цифры из реальных прогонов, с командами, которыми получены.
2. Дефолт в конфиге соответствует критерию из п.4.
3. Ни одной цифры «по памяти» или из этого плана — только измеренные.
4. `uv run pytest` зелёный.

---

## Task 3 — Многоходовый чат: намерение реплики и слияние разбора (ML-часть)

**Зачем.** Каждое сообщение уходит в `/search` как самостоятельный запрос.
«А подешевле», «убери первые этажи», «оставь только эти районы» не работают:
предыдущий `ParsedQuery` в контексте следующего шага не участвует, хотя лежит
в `chat_searches.parsed_query`. Для продукта, который называется агентом, это
главная функциональная дыра.

**Что сделать.**

1. В `habitus/online/schema.py`:
   ```python
   TurnIntent = Literal["new_search", "refine", "followup"]
   ```
   - `SearchRequest`: добавить `prev_parsed: ParsedQuery | None = None`.
   - `SearchResponse`: добавить `intent: TurnIntent = "new_search"`.
   - Новая модель разбора реплики:
     ```python
     class ParsedTurn(BaseModel):
         intent: TurnIntent = "new_search"
         query: ParsedQuery = ParsedQuery()
         cleared_fields: list[str] = []   # какие ограничения пользователь снял
     ```
     `cleared_fields` валидировать: допустимы только имена полей `ParsedQuery`;
     неизвестное имя — молча отбрасывается (LLM ошибается, ронять запрос нельзя).
2. В `habitus/online/nlu.py` добавить `parse_turn(text, llm, prev: ParsedQuery | None,
   max_retries: int = 3) -> ParsedTurn`:
   - `prev is None` → в промпт предыдущий разбор не кладём, intent всегда
     `new_search`, поведение эквивалентно текущему `parse_query`.
   - `prev` задан → в системный промпт добавляется блок с предыдущим разбором
     (JSON) и правила классификации:
     - `new_search` — человек ищет другое (сменились комнаты/район/бюджет так,
       что это новый запрос, либо явно «давай заново»);
     - `refine` — человек правит прошлый запрос («подешевле», «только с метро
       ближе», «убери шумные»); в `query` кладутся **только изменившиеся поля**,
       снятые ограничения — в `cleared_fields`;
     - `followup` — вопрос про уже показанную выдачу, поиск не нужен.
   - `parse_query` **оставить как есть** (её зовут eval и тесты) — реализовать
     `parse_turn` рядом, разделяя общий код промпта.
3. Слияние — отдельная чистая функция в `habitus/online/nlu.py`:
   ```python
   def merge_parsed(prev: ParsedQuery, turn: ParsedTurn) -> ParsedQuery:
   ```
   Правила, строго:
   - `turn.intent == "new_search"` → вернуть `turn.query` без слияния.
   - иначе: берём `prev`, поверх накладываем поля `turn.query`, у которых
     значение **не дефолтное** (не `None` и не пустой список/строка);
   - поля из `turn.cleared_fields` сбрасываются в дефолт `ParsedQuery`;
   - `semantic_text`: непустой новый заменяет старый, пустой — сохраняет старый;
   - `lang` берётся из `turn.query`.
4. В `habitus/online/pipeline.py`: `run_search` принимает
   `prev_parsed: ParsedQuery | None = None`; при наличии LLM зовёт `parse_turn`,
   считает `pq = merge_parsed(prev_parsed, turn)`, кладёт `intent` в ответ.
   Деградация NLU (нет LLM / `ParseError` / `LLMUnavailable`) — как сейчас:
   `ParsedQuery(semantic_text=query)`, `intent="new_search"`, `degraded += ["nlu"]`.
   `intent == "followup"` при этом ничего в пайплайне не сокращает — ветку
   «не искать» решает шлюз (Task 4); ML честно отдаёт разбор и выдачу.
5. `habitus/online/service.py`: `/search` прокидывает `req.prev_parsed`.

**Файлы.** `habitus/online/schema.py`, `habitus/online/nlu.py`,
`habitus/online/pipeline.py`, `habitus/online/service.py`,
`tests/test_nlu.py`, `tests/test_pipeline.py`, `tests/test_online_schema.py`.

**Тесты (обязательный минимум, всё на `FakeLLM` — без сети).**
- `merge_parsed`: refine меняет только присланное поле, остальное из `prev` цело.
- `merge_parsed`: `cleared_fields=["noise_max"]` сбрасывает шум в `None`.
- `merge_parsed`: `new_search` игнорирует `prev` полностью.
- `merge_parsed`: пустой `semantic_text` не затирает прошлый.
- `cleared_fields` с несуществующим именем поля не роняет разбор.
- `parse_turn` без `prev` эквивалентен `parse_query` по полям `query`.
- `run_search` с `prev_parsed` и подставным LLM, вернувшим `refine`, ищет по
  слитому разбору (проверяется через подставной `search_fn`/фейковый conn).
- Деградация: `llm=None` + `prev_parsed` задан → `intent="new_search"`,
  `degraded` содержит `"nlu"`.

**Приёмка.**
1. `uv run pytest` зелёный.
2. Старый вызов `run_search(query, conn, llm=llm)` без `prev_parsed` работает
   ровно как раньше (есть тест, который это стережёт).
3. `git show --stat HEAD` — только файлы из списка.

---

## Task 4 — Многоходовый чат: предыдущий разбор в шлюзе (Go-часть)

**Зачем.** ML умеет принимать `prev_parsed` (Task 3), но шлюз шлёт только текст.

**Что сделать.**

1. `backend/internal/client/ml_client.go`: в `SearchRequest` добавить
   `PrevParsed map[string]any \`json:"prev_parsed,omitempty"\``, в
   `SearchResponse` — `Intent string \`json:"intent"\``. Существующие поля
   не трогать.
2. `backend/internal/repository/chat_search_repo.go`: метод, отдающий
   `parsed_query` последнего поиска чата (`ORDER BY created_at DESC LIMIT 1`).
   Если поисков в чате не было — вернуть «нет данных» без ошибки.
3. `backend/internal/service/search_stream_service.go`: перед вызовом
   `s.ml.Search` прочитать последний разбор чата и положить его в
   `SearchRequest.PrevParsed`. Ошибка чтения — **не фатальна**: логируем и идём
   без контекста (деградация, а не 500).
4. Сохранять `intent` ответа: миграция `0009_chat_searches_intent.up.sql` /
   `.down.sql`, колонка `intent TEXT`, запись в `persist`. Значение по
   умолчанию не выдумываем: нет поля в ответе ML — колонка остаётся `NULL`.
5. В событие `final_result` добавить поле `intent` (аддитивно, фронт его
   может игнорировать).

**Файлы.** `backend/internal/client/ml_client.go`,
`backend/internal/repository/chat_search_repo.go`,
`backend/internal/service/search_stream_service.go`,
`backend/migrations/0009_chat_searches_intent.up.sql`,
`backend/migrations/0009_chat_searches_intent.down.sql`,
и соответствующие `_test.go`.

**Тесты.**
- Подставной репозиторий отдаёт прошлый разбор → в теле запроса к ML есть
  `prev_parsed` (проверять по сериализованному JSON).
- Первый поиск в чате → `prev_parsed` в теле отсутствует.
- Ошибка чтения прошлого разбора → поиск всё равно выполняется, событий `error` нет.
- `intent` из ответа ML доезжает до `final_result`.

**Приёмка.**
1. `cd backend && go test ./...` зелёный, тестов стало больше.
2. Миграция накатывается и откатывается.
3. Тесты не ходят в реальную БД и сеть.

---

## Task 5 — Честность полей с низким покрытием: окна и инсоляция

**Зачем.** `window_orientation` заполнен у 64 объявлений из 3291 (1.9%), но NLU
парсит «окна на юго-запад» в жёсткий фильтр `window_orientation && %s`. Результат:
почти любой такой запрос сначала отсекает 98% базы, потом relaxation снимает
фильтр и рапортует «снят фильтр ориентации окон». Пользователю показывают
ослабление там, где на самом деле нет данных. Колонка `insolation_rough`
заполнена у **нуля** объектов, при этом досье обещает блок с инсоляцией.

**Что сделать.**

1. `habitus/online/retrieval.py::build_where` — убрать клаузу
   `window_orientation && %s`. Ориентация больше не режет выборку.
2. `habitus/config.py` — новый параметр:
   ```python
   # Вес совпадения ориентации окон в финальном бленде. Не фильтр: данные об
   # ориентации есть у ~2% объявлений (извлекаются из прозы описания, см.
   # habitus/clean/windows.py), поэтому жёсткое условие отсекало базу целиком
   # и подменялось relaxation-ом на каждом запросе.
   orientation_weight: float = 0.15
   ```
3. `habitus/online/rerank.py::proximity_rerank` — к финальному бленду прибавлять
   `settings.orientation_weight`, если у кандидата в `facts["window_orientation"]`
   есть хотя бы одно из запрошенных `pq.window_orientation` направлений.
   Срабатывает только когда `pq.window_orientation` непуст. Отсутствие данных —
   не штраф и не бонус (0), а не «плохая ориентация».
4. `habitus/online/orchestrator.py::relax` — убрать шаг ослабления
   `window_orientation` (ослаблять нечего, фильтра больше нет).
5. Честное покрытие наружу. В `SearchResponse` добавить
   `notes: list[str] = []`. Когда `pq.window_orientation` непуст, pipeline
   добавляет строку с **реально посчитанным** покрытием, например:
   «данные об ориентации окон есть у 64 из 3291 объявлений (1.9%) — учли как
   предпочтение, а не как фильтр». Покрытие считается одним SQL-запросом по
   `listings` (город из запроса, `is_active = TRUE`). Не удалось посчитать —
   заметки нет; выдуманных процентов быть не должно.
6. `habitus/online/explain.py` — строки из `notes` передаются в блок ФАКТОВ
   (как это сделано для `ОСЛАБЛЕНО`), чтобы объяснение могло на них опереться,
   и **только** оттуда. Системный промпт дополнить: если в ФАКТАХ есть
   ПРИМЕЧАНИЕ — упомянуть его честно.
7. Инсоляция: убедиться, что при `insolation_rough IS NULL` и отсутствии данных
   блок `ViewClimateData` в `habitus/online/dossier.py` **деградирует**
   (блока нет или он secondary без выдуманных чисел), а не публикует нули.
   Добавить тест, который это стережёт. Если код уже деградирует правильно —
   тест всё равно нужен, он фиксирует поведение.

**Файлы.** `habitus/config.py`, `habitus/online/retrieval.py`,
`habitus/online/rerank.py`, `habitus/online/orchestrator.py`,
`habitus/online/pipeline.py`, `habitus/online/schema.py`,
`habitus/online/explain.py`, `tests/test_retrieval.py`, `tests/test_rerank.py`,
`tests/test_orchestrator.py`, `tests/test_explain.py`, `tests/test_dossier.py`.

**Тесты.**
- `build_where` с `window_orientation` не добавляет клаузу и не добавляет параметр.
- `proximity_rerank`: при совпадении ориентации кандидат поднимается выше
  равного ему по остальным сигналам; при отсутствии данных — не опускается ниже
  того, у кого ориентация не совпала.
- `relax` больше не возвращает шаг про ориентацию.
- `notes` попадают в блок фактов объяснения.
- Досье без `insolation_rough` не публикует синтетических часов солнца.

**Приёмка.**
1. `uv run pytest` зелёный.
2. Ни в одном тесте нет ожидания «ноль вместо отсутствующего значения».
3. `git show --stat HEAD` — только файлы из списка.

---

## Task 6 — Пустая выдача с диагностикой и запас результатов для пагинации (ML)

**Зачем.** Сейчас при пустой выдаче пользователь получает список ослаблений и
всё. Непонятно, какое именно условие убило выборку. И `rerank_top_n=10` жёстко —
«показать ещё» невозможно, хотя пул уже посчитан.

**Что сделать.**

1. Диагностика ограничений. В `habitus/online/retrieval.py`:
   ```python
   def constraint_diagnostics(conn, pq: ParsedQuery, geo_sql: str | None = None,
                              geo_params: Sequence = (),
                              city: str | None = None) -> list[dict]:
   ```
   Считает, сколько объектов остаётся при **последовательном** добавлении клауз
   в том же порядке, что в `build_where`: база (`is_active` + город) → цена →
   комнаты → площадь → гео-минуты → шум → стоп-факторы → гео-предикат области.
   Возвращает список `{"constraint": "<человекочитаемая метка>", "remaining": <int>}`.
   Только `COUNT(*)`, параметризованно, без склейки строк.
2. В `SearchResponse` добавить `diagnostics: list[dict] = []`. Заполняется
   **только когда итоговая выдача пуста** — на каждом запросе лишние COUNT'ы не нужны.
3. Запас результатов. В `habitus/config.py`:
   ```python
   # Сколько объектов возвращает /search сверх первой страницы: шлюз сохраняет
   # весь набор и отдаёт «показать ещё» из своей таблицы, не гоняя поиск заново.
   result_max_n: int = 30
   ```
   `SearchRequest` получает `top_n: int | None = None` (валидация: `gt=0, le=50`).
   `run_search` берёт `top_n or settings.result_max_n` вместо
   `settings.rerank_top_n` при финальном срезе в `proximity_rerank`.
   `settings.rerank_top_n` остаётся дефолтом размера **страницы** и в этой задаче
   не меняется по смыслу — просто перестаёт быть потолком выдачи.
4. Объяснение считается по первым `settings.rerank_top_n` объектам, а не по всем
   30: в промпт объяснения не должен уезжать весь набор (`explain.facts_block`).

**Файлы.** `habitus/config.py`, `habitus/online/retrieval.py`,
`habitus/online/pipeline.py`, `habitus/online/schema.py`,
`habitus/online/explain.py`, `tests/test_retrieval.py`, `tests/test_pipeline.py`,
`tests/test_explain.py`.

**Тесты.**
- `constraint_diagnostics` на тестовой БД: убийственное условие видно как резкое
  падение `remaining` до нуля (тест скипается без Postgres, как остальные БД-тесты).
- `diagnostics` пуст, когда выдача непуста.
- `run_search` с `top_n=25` возвращает не больше 25 объектов.
- В блок фактов объяснения уходит не больше `rerank_top_n` объектов.

**Приёмка.**
1. `uv run pytest` зелёный.
2. Ни один SQL не собран склейкой строк (проверить глазами диф).

---

## Task 7 — «Показать ещё» и срок жизни кэша досье (Go)

**Зачем.** ML теперь отдаёт до 30 объектов (Task 6), а шлюз показывает 10 и
остальное теряет. Досье кэшируется в `chat_search_results.dossier` навсегда:
данные объекта обновляются циклом сбора, а досье остаётся прошлогодним.

**Что сделать.**

1. `search_stream_service.go`: сохранять в `chat_search_results` **весь**
   набор объектов из ответа ML, а в SSE-событие `final_result` класть первые
   `resultPageSize = 10`. Добавить в событие поля `total` (сколько сохранено)
   и `has_more` (bool). Существующие поля события не трогать.
2. Новый маршрут `GET /api/v1/chats/:chat_id/results?offset=&limit=` (под
   авторизацией, с проверкой владения чатом — как в остальных ручках чата):
   отдаёт сохранённые объекты **последнего** поиска этого чата, отсортированные
   по `score DESC, external_id`, в том же формате объекта, что и `final_result`.
   - `limit` по умолчанию 10, больше 50 — обрезается до 50;
   - `offset` по умолчанию 0, отрицательный — 0;
   - кривое значение параметра игнорируется молча (как `parseBbox` в
     `geo_handler.go`), ошибки 400 из-за этого нет;
   - в ответе — `count` (сколько отдано) и `total` (сколько всего в поиске).
3. Срок жизни досье: `object_service.go` считает кэш `chat_search_results.dossier`
   протухшим, если `dossier_updated_at` старше `DossierTTLHours` (новый параметр
   конфига, env `DOSSIER_TTL_HOURS`, дефолт 24) **или** если
   `listings.updated_at` объекта новее `dossier_updated_at`. Протухший кэш —
   перезапрос к ML, как при отсутствии кэша.
4. `backend/internal/config/config.go`: добавить `DossierTTLHours int`.

**Файлы.** `backend/internal/http/router.go`,
`backend/internal/http/handlers/chat_handler.go` (или новый хендлер рядом),
`backend/internal/service/search_stream_service.go`,
`backend/internal/service/object_service.go`,
`backend/internal/repository/chat_search_repo.go`,
`backend/internal/config/config.go`, соответствующие `_test.go`.
**Не трогать** `geo_layers_service.go`, `listing_repo.go::ListInBBox`,
`health_handler.go` — они у другого разработчика.

**Тесты.**
- Разбор `offset`/`limit`: дефолты, обрезание до 50, кривые значения игнорируются.
- Чужой чат → 403/404 как в остальных ручках чата (повторить существующее поведение).
- Кэш досье свежее TTL — к ML не ходим; старше TTL — ходим.
- Досье старше `listings.updated_at` — перезапрашивается.
- `final_result` содержит 10 объектов и `has_more=true`, когда ML вернул больше.

**Приёмка.**
1. `cd backend && go test ./...` зелёный, тестов стало больше.
2. Таблица `listings` из Go по-прежнему только читается: ни `INSERT`, ни
   `UPDATE`, ни `DELETE`.

---

## Task 8 — Эксплуатация: закрыть ML-порт, ограничить расход LLM, отдать метрики (Go + compose)

**Зачем.** `ml-service` проброшен наружу портом 8000 без авторизации — это прямой
доступ к БД-запросам и к оплаченному ключу OpenRouter в обход шлюза. Рейт-лимита
нет вовсе: любой зарегистрированный пользователь жжёт бюджет через
`/messages/stream` и `/ask/stream`. Наблюдаемости нет: `trace.span` пишет
тайминги в лог ML-сервиса, и на этом всё.

**Что сделать.**

1. `docker-compose.yml`: убрать проброс `ports: - "8000:8000"` у `ml-service`.
   Внутри сети compose шлюз ходит по имени сервиса, healthcheck — по localhost
   внутри контейнера; оба продолжают работать. В `docker-compose.override.yml`
   (он gitignored, но лежит локально) проброс можно оставить для отладки —
   **файл не коммитить**. В `README.md` отметить, что порт 8000 наружу больше
   не смотрит и почему.
2. Рейт-лимит на LLM-ручки. Новый middleware
   `backend/internal/http/middleware/ratelimit.go`: счётчик в памяти по
   `user_id`, скользящее окно в час. Параметры конфига:
   `RATE_LIMIT_LLM_PER_HOUR` (дефолт 30). Применяется к
   `POST /chats/:chat_id/messages/stream` и
   `POST /objects/:object_id/ask/stream`. Превышение — HTTP 429 и честное
   сообщение по-русски о том, когда лимит восстановится. В комментарии явно
   написать, что счётчик в памяти и корректен для одной реплики (как и
   in-memory лок стрима в `search_stream_service.go`).
3. Метрики. Новый `GET /metrics` (без авторизации, как `/health`), формат
   Prometheus text exposition, **без новых зависимостей** — счётчики руками
   в `backend/internal/observability/`:
   - `habitus_http_requests_total{route,status}` — счётчик;
   - `habitus_ml_call_seconds{kind}` — сумма и количество (kind: `search`,
     `explain`, `dossier`, `object_ask`);
   - `habitus_ml_degraded_total{layer}` — сколько раз ML вернул слой в `degraded`;
   - `habitus_ml_stage_seconds{stage}` — сумма и количество из поля `timings`
     ответа ML (Task 1). Стадии, которых в ответе нет, не публикуем.
   - `habitus_rate_limited_total` — сколько раз сработал лимит.
   Метрики обновляются из уже существующих мест (middleware + сервисы), новых
   слоёв абстракции не заводить.

**Файлы.** `docker-compose.yml`, `README.md`,
`backend/internal/http/middleware/ratelimit.go`,
`backend/internal/observability/*.go`, `backend/internal/http/router.go`,
`backend/internal/app/app.go`, `backend/internal/config/config.go`,
`backend/internal/service/search_stream_service.go`,
`backend/internal/client/ml_client.go` (только чтение `timings`),
соответствующие `_test.go`.

**Тесты.**
- Лимит: N+1-й запрос в окне → 429; после сдвига окна → снова пропускает
  (время подставное, не `time.Sleep`).
- Лимит считается по пользователю, а не глобально.
- `/metrics` отдаёт валидный текстовый формат и содержит зарегистрированные счётчики.
- `/metrics` доступен без авторизации, остальной API не сломан.

**Приёмка.**
1. `cd backend && go test ./...` зелёный.
2. `docker compose config` не содержит проброса 8000 у `ml-service`.
3. `.env` и `docker-compose.override.yml` не в коммите.

---

## Task 9 — Щадящий режим сбора и текстовые запросы в golden-set с гейтом

**Зачем.** Источник заблокировал сборщик 17 августа при 5000 объявлений и 85
страницах подряд (`docs/notes/cian-blocked-2026-08-17.md`); рекомендованный
выход — ходить меньше и чаще. Golden-set из 16 запросов покрывает только
структурные оси, семантика не измеряется вовсе, а eval никем не запускается
автоматически.

**Что сделать.**

1. Щадящий режим. В `scripts/refresh.sh` изменить дефолт
   `MAX_OFFERS=${HABITUS_MAX_OFFERS:-5000}` на `400` и добавить комментарий
   со ссылкой на `docs/notes/cian-blocked-2026-08-17.md`: почему меньше и чаще
   безопаснее, и что четыре цикла в сутки дают тот же объём.
   **Код парсера (`backend/internal/cian/**`, `backend/cmd/cian-parser/`) не трогать.**
   В `docs/cian-parser.md` отразить новый дефолт.
2. Текстовые запросы в golden-set. Добавить в `habitus/eval/queries.yaml`
   серию `c01`–`c06` — запросы, которые **не решаются** SQL-фильтрами и
   proximity: район/улица в адресе («сталинка в Хамовниках»), станция метро по
   названию, тип дома, вайб из описания. Для каждого — `expected_parse` и
   `curate: true`.
3. Расширить `habitus/eval/curate.py`, чтобы правила умели строить эталон по
   текстовым осям: новое необязательное поле запроса `match` с условиями по
   колонкам `address` / `metro_station` (например
   `{"address_ilike": "%Хамовник%"}`), которые добавляются в `eligible_rows`
   параметризованно (`ILIKE %s`, никакой склейки). Оси, которые правилами не
   строятся (чистый вайб), в c-серию не включать — честнее не иметь запроса,
   чем иметь фиктивный эталон.
4. Перекурировать golden-set (`uv run python -m habitus.eval.curate`) и
   закоммитить обновлённый `queries.yaml`.
5. Гейт. `uv run habitus eval` получает флаг `--check` с порогами:
   `--min-recall` (дефолт — текущее измеренное значение минус 0.05) и
   `--min-ndcg`. При недоборе — ненулевой код возврата и понятное сообщение,
   какая метрика и насколько просела. Пороги и способ запуска описать в
   `README.md` (раздел про eval).
6. Прогнать eval после расширения набора и записать новую базовую линию в
   `docs/notes/eval-baseline-2026-08-18.md`: отдельно метрики по a-серии
   (структурные) и по c-серии (текстовые) — смешивать их в одно число
   бессмысленно, они меряют разные стадии.

**Файлы.** `scripts/refresh.sh`, `docs/cian-parser.md`,
`habitus/eval/queries.yaml`, `habitus/eval/curate.py`, `habitus/eval/runner.py`,
`habitus/cli.py`, `README.md`, `docs/notes/eval-baseline-2026-08-18.md`,
`tests/test_eval.py`, `tests/test_eval_curate.py`.

**Тесты.**
- `--check` возвращает ненулевой код при метрике ниже порога и нулевой — выше
  (на подставном отчёте, без реального прогона).
- Новое поле `match` в curate строится параметризованно (тест на построенный SQL
  и список параметров).
- Разбивка метрик по сериям запросов считается корректно.

**Приёмка.**
1. `uv run pytest` зелёный.
2. В `queries.yaml` c-серия имеет непустые `relevant_ids` после курирования.
3. В заметке — реальные измеренные цифры, отдельно по сериям.
4. Дефолт `MAX_OFFERS` в `scripts/refresh.sh` равен 400, код сборщика не изменён.
