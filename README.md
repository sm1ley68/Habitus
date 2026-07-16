# Habitus

## Локальный запуск

1. Создайте локальный файл с секретами:

   ```bash
   cp .env.example .env
   ```

2. Заполните `OPENROUTER_API_KEY` и `ORS_API_KEY` в `.env`. Пустые значения
   допустимы для деградированного режима, но LLM и точные ORS-изохроны в нём
   недоступны.

3. Запустите стек:

   ```bash
   docker compose up --build
   ```

При первом запуске сервис `ml-model-cache` скачает BGE-M3 и reranker (около
4,6 ГБ) в Docker volume `habitus_hf`. Следующие запуски переиспользуют этот
кэш. `ml-service` стартует только после успешной загрузки моделей, а backend
открывает HTTP-порт после синхронного прогрева ML.

Для другого env-файла используйте `docker compose --env-file <path> up`.
В production передавайте те же переменные через секрет-хранилище платформы;
реальные ключи не должны попадать в Compose-файл или Git.

## Данные для dossier

Контуры зданий/парков/воды для расчёта вида и теней загружаются отдельно:

```bash
uv run habitus import-osm-features
```

Точные слои коммунального фонда, преступности и шума импортируются из GeoJSON:

```bash
uv run habitus import-evidence --geojson data/moscow-evidence.geojson
```

Корень файла — `FeatureCollection`. Для каждого `Feature` обязательны свойства
`source_id`, `source`, `city: "msk"`, `layer`, `observed_at`. Слои `communal`
и `crime` принимают только Polygon/MultiPolygon и `weight` в диапазоне `0..1`;
`noise` принимает геометрию с неотрицательным `db`. Импорт идемпотентен по
`(source, source_id, layer)`. Пока точного слоя нет, API не подставляет ноль и
не повышает соответствующий блок dossier до `tier: "hero"`.

## Парсер Cian

Go-парсер объявлений с `description`, структурными полями, Chrome TLS-профилем,
ротацией прокси и инкрементальной CSV/JSON-выгрузкой находится в
`backend/cmd/cian-parser`. Инструкция по настройке и запуску:
[docs/cian-parser.md](docs/cian-parser.md).
