# Habitus

## Репозиторий и ветки

Работа идёт напрямую в `main`. Коммитить и пушить в `main`, отдельные ветки не заводить.

Формат сообщений коммитов — Conventional Commits на русском:
`feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `eval:`, `docs:`.

Подписи и трейлеры в коммитах не используются. Никаких `Co-Authored-By`.

## Архитектура

Три слоя, поднимаются одной командой `docker compose up`:

- **`habitus/`** — Python/FastAPI ML-сервис (порт 8000). Внутренний, наружу не смотрит.
  Отдаёт `POST /search`, `POST /dossier`, `POST /object-ask`.
  Единственный источник правды по формам данных — `habitus/online/schema.py`.
- **`backend/`** — Go/Fiber шлюз (порт 8080). Публичный API `/api/v1`.
  Ходит в ML-сервис через `internal/client/ml_client.go`, хранит чаты/сессии в Postgres.
  Роуты — `internal/http/router.go`.
- **`frontend/`** — Next.js (порт 3000). Ходит только в Go-шлюз.

БД — Postgres 16 + PostGIS + pgvector (`Dockerfile.db`), порт 5544 наружу.

## Контракт

`frontend/Пайплайн фронт.md` — первичный источник по API. `Пайплайн бэк — изменения.md` —
выжимка по досье объекта. Enum'ы зафиксированы на трёх сторонах:
`habitus/online/schema.py` ↔ Go `internal/service/` ↔ `frontend/lib/agent/types.ts`.

Координаты **везде** `[lng, lat]`, WGS84 (EPSG:4326). Без трансформаций на фронте.

## Ключевые правила

**Не выдумывать факты о городе.** Если данных по объекту нет — блок деградирует до
secondary или отсутствует. Синтетический ноль вместо отсутствующего замера —
запрещён (см. `habitus/online/dossier.py`, `backend/internal/service/display_fields.go`).
Имена объектов синтезируются только из структурных фактов (`SynthName`).

**Секреты не коммитить.** `.env` в `.gitignore`, шаблон — `.env.example`.

## Данные

Оффлайн-пайплайн: `uv run habitus offline --csv <path>`.
Поиск из CLI: `uv run habitus search "запрос"`. Метрики: `uv run habitus eval`.

## Тесты

- Python: `uv run pytest`
- Go: `cd backend && go test ./...`
- Frontend: `cd frontend && npm test`
