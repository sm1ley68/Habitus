# Личный кабинет продавца + импорт с Циана — план реализации

> **Для агентов-исполнителей:** ОБЯЗАТЕЛЬНЫЙ САБ-СКИЛЛ: используйте
> superpowers:subagent-driven-development (рекомендуется) или
> superpowers:executing-plans, чтобы выполнять план задача за задачей.
> Шаги размечены чекбоксами (`- [ ]`).

**Goal:** Дать продавцу квартиры кабинет, в котором он импортирует своё
объявление с Циана по ссылке (или заполняет карточку с нуля) и публикует его в
общий поиск Habitus.

**Architecture:** Go — система учёта продавца (таблица `owner_listings`:
владение, статусы, черновики, фото). Python — витрина: две новые ручки кладут
опубликованное объявление в `listings`, точечно обогащают и индексируют BGE-M3.
Go зовёт Python при публикации через существующий `internal/client/ml_client.go`.
Граница владения БД сохраняется: Go по-прежнему не пишет в Python-схему.

**Tech Stack:** Go 1.25 / Fiber v2 / pgx v5 / golang-migrate; Python 3 / FastAPI /
psycopg 3 / PostGIS / pgvector / BGE-M3; Next.js 15 App Router / React 19 /
Tailwind 3 / zustand / maplibre-gl / Vitest.

**Spec:** `docs/superpowers/specs/2026-08-23-owner-cabinet-design.md`

## Global Constraints

- Ветка одна — `main`. Коммиты Conventional Commits **на русском**
  (`feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`). **Без трейлеров**,
  никаких `Co-Authored-By`.
- Координаты **везде** `[lng, lat]`, WGS84 (EPSG:4326). Без трансформаций на фронте.
- **Не выдумывать факты о городе.** Нет данных — блок деградирует или
  отсутствует. Синтетический ноль вместо отсутствующего замера запрещён.
  В кабинете это означает: **никаких счётчиков просмотров и звонков** — таких
  данных в системе нет.
- Границы владения БД: Go владеет `backend/migrations/`, Python владеет
  `habitus/db/schema.sql` (`listings`, `raw_listings`, `poi`, `urban_evidence`, …).
  Go читает Python-таблицы только на чтение и никогда в них не пишет.
- `raw_listings` объявлениями продавца **не трогаем** — это зеркало обхода
  источника.
- Enum'ы фиксируются на трёх сторонах: `habitus/online/schema.py` ↔
  `backend/internal/service/` ↔ `frontend/lib/agent/types.ts`.
- Формат ошибок API — существующий конверт `{"error":{"code","message"}}`
  (`backend/internal/http/middleware/errorenvelope.go`). Хендлер возвращает
  `*apperr.Error`, не пишет JSON сам.
- Тесты с БД **скипаются**, а не падают, когда Postgres недоступен (Python —
  `tests/conftest.py`, Go — `internal/repository/main_test.go:42` `testPool`).
- **Живого Циана в тестах нет** — только зафиксированный JSON в фикстурах.
- Секреты не коммитить; новые переменные идут в `.env.example`.
- Значения по умолчанию для новых переменных: `CIAN_FETCH_PER_MIN=6`,
  `OWNER_IMPORT_PER_HOUR=20`, `OWNER_AUTOPUBLISH=true`, `ML_OWNER_TIMEOUT_S=60`,
  `OWNER_PHOTO_MAX_MB=10`, `OWNER_PHOTO_MAX_COUNT=20`.
- Команды тестов: `uv run pytest`, `cd backend && go test ./...`,
  `cd frontend && npm test`.

---

## Слой 1 — схема и защита батч-пайплайна

### Task 1: `owner_managed` и защита пайплайна от затирания правок

**Files:**
- Modify: `habitus/db/schema.sql` (в блок ALTER'ов в конце файла)
- Modify: `habitus/update/incremental.py:108-110` (выборка в `deactivate_missing`)
- Modify: `habitus/clean/normalize.py:57-66` (`ON CONFLICT DO UPDATE` в `promote_to_listings`)
- Test: `tests/test_owner_managed.py`

**Interfaces:**
- Consumes: ничего
- Produces: колонка `listings.owner_managed boolean NOT NULL DEFAULT false` —
  на неё опираются Task 5 (ставит `true`) и Task 10 (читает при дедупе).

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_owner_managed.py`:

```python
import psycopg
from psycopg.types.json import Json

from habitus.config import settings
from habitus.clean.normalize import promote_to_listings
from habitus.db.init_db import init_db
from habitus.update.incremental import deactivate_missing

MSK = (37.62, 55.75)


def _fresh(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings, raw_listings CASCADE;")
    conn.commit()


def _insert_owner(conn, external_id="owner_a1", price=9_000_000):
    with conn.cursor() as cur:
        cur.execute(
            """INSERT INTO listings (external_id, source, price, area, rooms,
                                     geom, city, owner_managed)
               VALUES (%s, 'owner', %s, 55.0, 2,
                       ST_SetSRID(ST_MakePoint(%s, %s), 4326), 'msk', true);""",
            (external_id, price, MSK[0], MSK[1]))
    conn.commit()


def test_deactivate_missing_spares_owner_managed():
    """Обход Циана не должен гасить объявление продавца: его нет и не может
    быть ни в одном снимке источника."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _fresh(conn)
        _insert_owner(conn)
        with conn.cursor() as cur:
            cur.execute(
                """INSERT INTO listings (external_id, source, price, area, rooms,
                                         geom, city)
                   VALUES ('cian_1', 'cian', 1e7, 40.0, 1,
                           ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 'msk'),
                          ('cian_2', 'cian', 1e7, 41.0, 1,
                           ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 'msk');""")
        conn.commit()

        deactivate_missing({"cian_1"}, conn, source="cian")

        with conn.cursor() as cur:
            cur.execute("SELECT external_id FROM listings WHERE is_active ORDER BY 1;")
            active = [r[0] for r in cur.fetchall()]
    assert active == ["cian_1", "owner_a1"]


def test_promote_does_not_overwrite_owner_edits():
    """Продавец привязал спарсенный объект и поправил цену. Следующий обход
    Циана приносит старую цену — правка должна пережить его."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _fresh(conn)
        with conn.cursor() as cur:
            cur.execute(
                """INSERT INTO listings (external_id, source, price, area, rooms,
                                         geom, city, owner_managed)
                   VALUES ('cian_777', 'cian', 12_000_000, 50.0, 2,
                           ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 'msk', true);""")
            cur.execute(
                """INSERT INTO raw_listings (external_id, source, price, area, rooms,
                                             lat, lon, city, source_extra)
                   VALUES ('cian_777', 'cian', 20_000_000, 50.0, 2,
                           55.7, 37.6, 'msk', %s);""", (Json({}),))
        conn.commit()

        promote_to_listings(conn)

        with conn.cursor() as cur:
            cur.execute("SELECT price FROM listings WHERE external_id='cian_777';")
            price = cur.fetchone()[0]
    assert price == 12_000_000
```

- [ ] **Step 2: Убедиться, что тест падает**

Запустить: `uv run pytest tests/test_owner_managed.py -v`
Ожидается: FAIL — `psycopg.errors.UndefinedColumn: column "owner_managed" does not exist`.
(Если Postgres не поднят — тест скипнется; тогда поднять `docker compose up -d db`.)

- [ ] **Step 3: Добавить колонку в схему**

В `habitus/db/schema.sql`, в конец файла к остальным идемпотентным ALTER'ам:

```sql
-- Объявление, которым управляет продавец через личный кабинет. Помечает
-- строки, на которые не распространяются два механизма батч-пайплайна:
-- гашение по снимку источника (объявления продавца ни в каком снимке нет)
-- и перезапись полей при повторном обходе (правки продавца главнее источника).
ALTER TABLE listings ADD COLUMN IF NOT EXISTS owner_managed boolean NOT NULL DEFAULT false;
```

- [ ] **Step 4: Исключить владельческие строки из гашения**

В `habitus/update/incremental.py`, в `deactivate_missing`, заменить строку
формирования `where`:

```python
    # owner_managed исключается всегда: объявление продавца не приходит ни в
    # одном снимке источника, поэтому «его нет в снимке» ничего не означает.
    where = "is_active = true AND NOT owner_managed" + (" AND source = %s" if source else "")
```

- [ ] **Step 5: Не перетирать правки продавца при повторном обходе**

В `habitus/clean/normalize.py`, в конце `ON CONFLICT (external_id) DO UPDATE SET …`
блока, после `is_active=true, updated_at=now()` добавить условие:

```sql
           is_active=true, updated_at=now()
        WHERE NOT listings.owner_managed;
```

- [ ] **Step 6: Убедиться, что тесты проходят**

Запустить: `uv run pytest tests/test_owner_managed.py -v`
Ожидается: 2 passed.

- [ ] **Step 7: Прогнать весь Python-набор — регрессий быть не должно**

Запустить: `uv run pytest`
Ожидается: прежнее количество passed, 0 failed.

- [ ] **Step 8: Коммит**

```bash
git add habitus/db/schema.sql habitus/update/incremental.py habitus/clean/normalize.py tests/test_owner_managed.py
git commit -m "feat: объявления продавца защищены от гашения и перезаписи обходом"
```

---

### Task 2: Валидация координат по городам

**Files:**
- Modify: `habitus/clean/normalize.py:6-22` (`MSK_BBOX`, `is_valid`)
- Test: `tests/test_city_bbox.py`

**Interfaces:**
- Consumes: ничего
- Produces: `habitus.clean.normalize.CITY_BBOX: dict[str, tuple[float,float,float,float]]`
  и `is_valid(row: dict) -> bool`, читающая `row["city"]` (дефолт `"msk"`).
  Сигнатура `is_valid` **не меняется** — все существующие вызовы работают как раньше.
  Task 5 использует `is_valid` для валидации объявления продавца.

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_city_bbox.py`:

```python
from habitus.clean.normalize import CITY_BBOX, MSK_BBOX, is_valid


def _row(lon, lat, city=None):
    row = {"price": 10_000_000, "area": 50.0, "lat": lat, "lon": lon}
    if city is not None:
        row["city"] = city
    return row


def test_msk_bbox_alias_preserved():
    """Старое имя остаётся: на него ссылается код и тесты пайплайна."""
    assert MSK_BBOX == CITY_BBOX["msk"]


def test_row_without_city_is_validated_as_moscow():
    """Батч-пайплайн Циана не проставляет city в каждую строку — дефолт msk."""
    assert is_valid(_row(37.62, 55.75)) is True
    assert is_valid(_row(30.31, 59.94)) is False


def test_spb_coordinates_valid_for_spb_row():
    assert is_valid(_row(30.31, 59.94, city="spb")) is True


def test_moscow_coordinates_invalid_for_spb_row():
    """Координаты чужого города — отказ: это опечатка или подмена, а не объект."""
    assert is_valid(_row(37.62, 55.75, city="spb")) is False


def test_unknown_city_rejected():
    assert is_valid(_row(37.62, 55.75, city="dxb")) is False
```

- [ ] **Step 2: Убедиться, что тест падает**

Запустить: `uv run pytest tests/test_city_bbox.py -v`
Ожидается: FAIL — `ImportError: cannot import name 'CITY_BBOX'`.

- [ ] **Step 3: Реализовать**

В `habitus/clean/normalize.py` заменить блок `MSK_BBOX` + `is_valid`:

```python
# Грубые bbox городов: lon_min, lat_min, lon_max, lat_max.
# msk — в пределах МКАД плюс Новая Москва небольшим запасом.
CITY_BBOX = {
    "msk": (37.30, 55.48, 37.95, 55.95),
    "spb": (29.60, 59.70, 30.70, 60.20),
}

# Историческое имя: на него ссылается код пайплайна и тесты.
MSK_BBOX = CITY_BBOX["msk"]


def is_valid(row: dict) -> bool:
    price = row.get("price") or 0
    area = row.get("area") or 0
    lat, lon = row.get("lat"), row.get("lon")
    if not (1_000_000 <= price <= 3_000_000_000):
        return False
    if not (5 <= area <= 1000):
        return False
    if lat is None or lon is None:
        return False
    # Строка без city — это выхлоп батч-пайплайна Циана, он московский.
    bbox = CITY_BBOX.get(row.get("city") or "msk")
    if bbox is None:
        return False
    lon_min, lat_min, lon_max, lat_max = bbox
    if not (lon_min <= lon <= lon_max and lat_min <= lat <= lat_max):
        return False
    return True
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Запустить: `uv run pytest tests/test_city_bbox.py -v`
Ожидается: 5 passed.

- [ ] **Step 5: Прогнать весь Python-набор**

Запустить: `uv run pytest`
Ожидается: 0 failed.

- [ ] **Step 6: Коммит**

```bash
git add habitus/clean/normalize.py tests/test_city_bbox.py
git commit -m "feat: валидация координат объявления по bbox своего города"
```

---

### Task 3: Таблица `owner_listings` и репозиторий

**Files:**
- Create: `backend/migrations/0010_owner_listings.up.sql`
- Create: `backend/migrations/0010_owner_listings.down.sql`
- Create: `backend/internal/domain/owner_listing.go`
- Create: `backend/internal/repository/owner_listing_repo.go`
- Test: `backend/internal/repository/owner_listing_repo_test.go`

**Interfaces:**
- Consumes: `repository.ErrNotFound`, харнесс `testPool(t)` из
  `backend/internal/repository/main_test.go:42`
- Produces:
  - `domain.OwnerListing` — структура со всеми полями таблицы
  - `domain.OwnerListingFields` — подмножество редактируемых полей (для `Update`)
  - `repository.OwnerListingRepo` с методами:
    `NewOwnerListingRepo(pool *pgxpool.Pool) *OwnerListingRepo`,
    `Create(ctx, l domain.OwnerListing) (domain.OwnerListing, error)`,
    `GetOwned(ctx, id, userID uuid.UUID) (domain.OwnerListing, error)`,
    `GetByExternalID(ctx, externalID string) (domain.OwnerListing, error)`,
    `List(ctx, userID uuid.UUID) ([]domain.OwnerListing, error)`,
    `UpdateFields(ctx, id, userID uuid.UUID, f domain.OwnerListingFields) (domain.OwnerListing, error)`,
    `SetStatus(ctx, id uuid.UUID, status, importError string) error`,
    `SetPhotos(ctx, id, userID uuid.UUID, photos []string) (domain.OwnerListing, error)`,
    `Delete(ctx, id, userID uuid.UUID) error`,
    `ErrExternalIDTaken` — сентинел на конфликт `owner_listings_external_id_key`.
  Всё это потребляют Task 10, 11, 12.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/repository/owner_listing_repo_test.go`:

```go
package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

func newTestUser(t *testing.T, r *UserRepo) uuid.UUID {
	t.Helper()
	u, err := r.Create(context.Background(), uuid.NewString()+"@example.test", "hash", "Продавец")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func sampleListing(userID uuid.UUID, externalID string) domain.OwnerListing {
	price := int64(12_500_000)
	area := float32(54.3)
	rooms := 2
	return domain.OwnerListing{
		UserID:      userID,
		ExternalID:  externalID,
		Origin:      "cian",
		Status:      "draft",
		City:        "msk",
		Price:       &price,
		Area:        &area,
		Rooms:       &rooms,
		Address:     "Москва, улица Мельникова, 3к1",
		Lng:         37.6595,
		Lat:         55.7108,
		Description: "Тихая двушка окнами во двор",
		Photos:      []string{"https://images.cdn-cian.ru/images/1.jpg"},
		SourceURL:   "https://www.cian.ru/sale/flat/319800087/",
	}
}

func TestOwnerListingCreateAndGetOwned(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(userID, "cian_319800087"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("ожидался сгенерированный id")
	}
	if created.Status != "draft" || created.Verification != "unverified" {
		t.Fatalf("дефолты не применились: %+v", created)
	}

	got, err := repo.GetOwned(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("get owned: %v", err)
	}
	if got.ExternalID != "cian_319800087" || *got.Price != 12_500_000 {
		t.Fatalf("неверные данные: %+v", got)
	}
}

func TestOwnerListingGetOwnedHidesForeign(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	owner, stranger := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(owner, "cian_1001"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.GetOwned(ctx, created.ID, stranger); !errors.Is(err, ErrNotFound) {
		t.Fatalf("чужой объект должен быть неотличим от несуществующего, получено %v", err)
	}
}

func TestOwnerListingExternalIDIsTaken(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	first, second := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	if _, err := repo.Create(ctx, sampleListing(first, "cian_2002")); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := repo.Create(ctx, sampleListing(second, "cian_2002"))
	if !errors.Is(err, ErrExternalIDTaken) {
		t.Fatalf("ожидался ErrExternalIDTaken, получено %v", err)
	}
}

func TestOwnerListingUpdateFields(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(userID, "cian_3003"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newPrice := int64(11_000_000)
	updated, err := repo.UpdateFields(ctx, created.ID, userID, domain.OwnerListingFields{
		Price:       &newPrice,
		Description: strptr("Снизил цену"),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if *updated.Price != 11_000_000 || updated.Description != "Снизил цену" {
		t.Fatalf("правка не применилась: %+v", updated)
	}
	if updated.Area == nil || *updated.Area != 54.3 {
		t.Fatalf("непереданные поля не должны обнуляться: %+v", updated)
	}
}

func TestOwnerListingListIsScopedToUser(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	mine, other := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	if _, err := repo.Create(ctx, sampleListing(mine, "cian_4004")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Create(ctx, sampleListing(other, "cian_5005")); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.List(ctx, mine)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ExternalID != "cian_4004" {
		t.Fatalf("список должен содержать только свои объявления: %+v", list)
	}
}

func strptr(s string) *string { return &s }
```

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/repository/ -run TestOwnerListing -v`
Ожидается: FAIL — `undefined: NewOwnerListingRepo`, `undefined: domain.OwnerListing`.

- [ ] **Step 3: Написать миграцию**

Создать `backend/migrations/0010_owner_listings.up.sql`:

```sql
-- Объявления, которыми продавец управляет через личный кабинет.
-- Это система учёта Go-стороны: владение, статусы, черновики, фото.
-- Витрина (Python-таблица listings) наполняется отдельно, при публикации.
CREATE TABLE owner_listings (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- UNIQUE — это и есть механизм «привязано к другому аккаунту»: конфликт
    -- вставки разрешает гонку двух одновременных импортов одной ссылки
    -- без проверки-перед-вставкой и без блокировок.
    external_id  text NOT NULL UNIQUE,
    origin       text NOT NULL CHECK (origin IN ('cian', 'manual')),
    status       text NOT NULL DEFAULT 'draft'
                 CHECK (status IN ('draft', 'publishing', 'published', 'unpublished', 'failed')),
    verification text NOT NULL DEFAULT 'unverified'
                 CHECK (verification IN ('unverified', 'verified')),
    city         text NOT NULL CHECK (city IN ('spb', 'msk')),

    price        bigint,
    area         real,
    kitchen_area real,
    rooms        integer,
    level        integer,
    levels       integer,
    address      text NOT NULL DEFAULT '',
    lng          double precision,
    lat          double precision,
    window_orientation text[] NOT NULL DEFAULT '{}',
    description  text NOT NULL DEFAULT '',
    photos       text[] NOT NULL DEFAULT '{}',

    source_url   text NOT NULL DEFAULT '',
    import_error text NOT NULL DEFAULT '',

    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

CREATE INDEX owner_listings_user_ix ON owner_listings (user_id, updated_at DESC);
```

Создать `backend/migrations/0010_owner_listings.down.sql`:

```sql
DROP TABLE IF EXISTS owner_listings;
```

- [ ] **Step 4: Написать доменную структуру**

Создать `backend/internal/domain/owner_listing.go`:

```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// OwnerListing — объявление, которым продавец управляет через личный кабинет.
// Указатели у числовых полей отличают «не заполнено» от нуля: черновик может
// не иметь цены, и это не то же самое, что цена 0.
type OwnerListing struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ExternalID   string
	Origin       string
	Status       string
	Verification string
	City         string

	Price             *int64
	Area              *float32
	KitchenArea       *float32
	Rooms             *int
	Level             *int
	Levels            *int
	Address           string
	Lng               float64
	Lat               float64
	WindowOrientation []string
	Description       string
	Photos            []string

	SourceURL   string
	ImportError string

	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt *time.Time
}

// OwnerListingFields — редактируемая часть. nil означает «поле не передано» и
// сохраняет прежнее значение; так PATCH не обнуляет то, чего в запросе не было.
type OwnerListingFields struct {
	Price             *int64
	Area              *float32
	KitchenArea       *float32
	Rooms             *int
	Level             *int
	Levels            *int
	Address           *string
	Lng               *float64
	Lat               *float64
	WindowOrientation *[]string
	Description       *string
	City              *string
}
```

- [ ] **Step 5: Написать репозиторий**

Создать `backend/internal/repository/owner_listing_repo.go`:

```go
package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

// ErrExternalIDTaken — объявление с таким external_id уже привязано к кабинету
// (своему или чужому). Сервис превращает его в 409 listing_claimed_by_other.
var ErrExternalIDTaken = errors.New("owner listing external_id already taken")

// ownerListingColumns перечисляет колонки в том же порядке, в каком их читает
// scanOwnerListing. Держать эти два списка синхронными — единственное
// требование к любому новому запросу в этом файле.
const ownerListingColumns = `id, user_id, external_id, origin, status, verification, city,
	price, area, kitchen_area, rooms, level, levels,
	address, coalesce(lng, 0), coalesce(lat, 0),
	window_orientation, description, photos,
	source_url, import_error, created_at, updated_at, published_at`

type OwnerListingRepo struct {
	pool *pgxpool.Pool
}

func NewOwnerListingRepo(pool *pgxpool.Pool) *OwnerListingRepo {
	return &OwnerListingRepo{pool: pool}
}

func scanOwnerListing(row pgx.Row) (domain.OwnerListing, error) {
	var l domain.OwnerListing
	err := row.Scan(
		&l.ID, &l.UserID, &l.ExternalID, &l.Origin, &l.Status, &l.Verification, &l.City,
		&l.Price, &l.Area, &l.KitchenArea, &l.Rooms, &l.Level, &l.Levels,
		&l.Address, &l.Lng, &l.Lat, &l.WindowOrientation, &l.Description, &l.Photos,
		&l.SourceURL, &l.ImportError, &l.CreatedAt, &l.UpdatedAt, &l.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OwnerListing{}, ErrNotFound
	}
	return l, err
}

func (r *OwnerListingRepo) Create(ctx context.Context, l domain.OwnerListing) (domain.OwnerListing, error) {
	created, err := scanOwnerListing(r.pool.QueryRow(ctx, `
		INSERT INTO owner_listings
			(user_id, external_id, origin, city, price, area, kitchen_area,
			 rooms, level, levels, address, lng, lat, window_orientation,
			 description, photos, source_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING `+ownerListingColumns,
		l.UserID, l.ExternalID, l.Origin, l.City, l.Price, l.Area, l.KitchenArea,
		l.Rooms, l.Level, l.Levels, l.Address, l.Lng, l.Lat, l.WindowOrientation,
		l.Description, l.Photos, l.SourceURL))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.OwnerListing{}, ErrExternalIDTaken
	}
	return created, err
}

func (r *OwnerListingRepo) GetOwned(ctx context.Context, id, userID uuid.UUID) (domain.OwnerListing, error) {
	return scanOwnerListing(r.pool.QueryRow(ctx,
		`SELECT `+ownerListingColumns+` FROM owner_listings WHERE id = $1 AND user_id = $2`, id, userID))
}

func (r *OwnerListingRepo) GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error) {
	return scanOwnerListing(r.pool.QueryRow(ctx,
		`SELECT `+ownerListingColumns+` FROM owner_listings WHERE external_id = $1`, externalID))
}

func (r *OwnerListingRepo) List(ctx context.Context, userID uuid.UUID) ([]domain.OwnerListing, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+ownerListingColumns+` FROM owner_listings WHERE user_id = $1 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.OwnerListing{}
	for rows.Next() {
		l, err := scanOwnerListing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UpdateFields применяет только переданные поля: COALESCE($n, колонка)
// оставляет прежнее значение там, где в запросе был nil.
func (r *OwnerListingRepo) UpdateFields(ctx context.Context, id, userID uuid.UUID, f domain.OwnerListingFields) (domain.OwnerListing, error) {
	var wo any
	if f.WindowOrientation != nil {
		wo = *f.WindowOrientation
	}
	return scanOwnerListing(r.pool.QueryRow(ctx, `
		UPDATE owner_listings SET
			price = COALESCE($3, price),
			area = COALESCE($4, area),
			kitchen_area = COALESCE($5, kitchen_area),
			rooms = COALESCE($6, rooms),
			level = COALESCE($7, level),
			levels = COALESCE($8, levels),
			address = COALESCE($9, address),
			lng = COALESCE($10, lng),
			lat = COALESCE($11, lat),
			window_orientation = COALESCE($12, window_orientation),
			description = COALESCE($13, description),
			city = COALESCE($14, city),
			updated_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING `+ownerListingColumns,
		id, userID, f.Price, f.Area, f.KitchenArea, f.Rooms, f.Level, f.Levels,
		f.Address, f.Lng, f.Lat, wo, f.Description, f.City))
}

func (r *OwnerListingRepo) SetPhotos(ctx context.Context, id, userID uuid.UUID, photos []string) (domain.OwnerListing, error) {
	return scanOwnerListing(r.pool.QueryRow(ctx, `
		UPDATE owner_listings SET photos = $3, updated_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING `+ownerListingColumns, id, userID, photos))
}

// SetStatus — переход статусной машины. published_at ставится один раз, при
// первом переходе в published: это дата появления в витрине, а не последней правки.
func (r *OwnerListingRepo) SetStatus(ctx context.Context, id uuid.UUID, status, importError string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE owner_listings SET
			status = $2,
			import_error = $3,
			published_at = CASE WHEN $2 = 'published' AND published_at IS NULL
			                    THEN now() ELSE published_at END,
			updated_at = now()
		WHERE id = $1`, id, status, importError)
	return err
}

func (r *OwnerListingRepo) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM owner_listings WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 6: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/repository/ -run TestOwnerListing -v`
Ожидается: 5 passed. Если Postgres не поднят — 5 skipped; тогда
`docker compose up -d db` и повторить.

- [ ] **Step 7: Прогнать весь Go-набор**

Запустить: `cd backend && go vet ./... && go test ./...`
Ожидается: 0 failed.

- [ ] **Step 8: Коммит**

```bash
git add backend/migrations/0010_owner_listings.up.sql backend/migrations/0010_owner_listings.down.sql \
        backend/internal/domain/owner_listing.go backend/internal/repository/owner_listing_repo.go \
        backend/internal/repository/owner_listing_repo_test.go
git commit -m "feat: таблица и репозиторий объявлений продавца"
```

---

## Слой 2 — Python: приём объявления в витрину

### Task 4: Точечное обогащение и точечный эмбеддинг

**Files:**
- Modify: `habitus/geo/enrich.py:68-100` (`_ENRICH_SQL`), `:101-107` (`enrich_all`), `:109-115` (`enrich_around`)
- Modify: `habitus/embed/encode.py:90-110` (`embed_pending`)
- Modify: `habitus/embed/document.py` (добавить `refresh_doc_text`)
- Modify: `habitus/cli.py:20-29` (убрать дубль `_refresh_doc_text`)
- Test: `tests/test_scoped_enrich.py`

**Interfaces:**
- Consumes: `CITY_BBOX` из Task 2 не требуется; только существующие функции
- Produces:
  - `habitus.geo.enrich.enrich_ids(conn, external_ids: list[str]) -> int`
  - `habitus.embed.document.refresh_doc_text(conn, external_ids: list[str] | None = None) -> int`
  - `habitus.embed.encode.embed_pending(conn, model=None, external_ids: list[str] | None = None) -> int`
  Все три вызывает Task 5.

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_scoped_enrich.py`:

```python
import psycopg

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.document import refresh_doc_text
from habitus.geo.enrich import enrich_ids


def _two_listings(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings, poi CASCADE;")
        cur.execute("""
            INSERT INTO listings (external_id, source, price, area, rooms, geom, city, address)
            VALUES ('owner_scoped', 'owner', 1e7, 50.0, 2,
                    ST_SetSRID(ST_MakePoint(37.62, 55.75), 4326), 'msk', 'Москва, Тверская 1'),
                   ('cian_untouched', 'cian', 1e7, 60.0, 3,
                    ST_SetSRID(ST_MakePoint(37.63, 55.76), 4326), 'msk', 'Москва, Тверская 2');
            UPDATE listings SET updated_at = '2020-01-01', doc_text = NULL;
        """)
    conn.commit()


def test_enrich_ids_touches_only_requested_rows():
    """Точечное обогащение не должно переписывать всю таблицу: на 130k строк
    это минуты, а публикуется одно объявление."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _two_listings(conn)

        affected = enrich_ids(conn, ["owner_scoped"])

        with conn.cursor() as cur:
            cur.execute("""SELECT external_id, updated_at > '2021-01-01'
                           FROM listings ORDER BY external_id;""")
            touched = dict(cur.fetchall())
    assert affected == 1
    assert touched["owner_scoped"] is True
    assert touched["cian_untouched"] is False


def test_refresh_doc_text_scoped():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _two_listings(conn)

        refresh_doc_text(conn, ["owner_scoped"])

        with conn.cursor() as cur:
            cur.execute("""SELECT external_id, doc_text IS NOT NULL
                           FROM listings ORDER BY external_id;""")
            built = dict(cur.fetchall())
    assert built["owner_scoped"] is True
    assert built["cian_untouched"] is False


def test_refresh_doc_text_without_ids_covers_everything():
    """Батч-пайплайн зовёт без списка — поведение прежнее."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _two_listings(conn)

        count = refresh_doc_text(conn)

        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE doc_text IS NOT NULL;")
            built = cur.fetchone()[0]
    assert count == 2
    assert built == 2
```

- [ ] **Step 2: Убедиться, что тест падает**

Запустить: `uv run pytest tests/test_scoped_enrich.py -v`
Ожидается: FAIL — `ImportError: cannot import name 'enrich_ids'`.

- [ ] **Step 3: Добавить фильтр по id в `_ENRICH_SQL`**

В `habitus/geo/enrich.py`, в конце `_ENRICH_SQL`, заменить блок `WHERE`:

```sql
WHERE l.geom IS NOT NULL
  AND (%(filter_ids)s::text[] IS NULL OR l.external_id = ANY(%(filter_ids)s::text[]))
  AND (%(filter_geog)s::text IS NULL
       OR ST_DWithin(l.geom::geography, ST_GeogFromText(%(filter_geog)s::text), %(radius)s));
```

- [ ] **Step 4: Провести новый параметр через существующие обёртки и добавить `enrich_ids`**

В `habitus/geo/enrich.py` заменить `enrich_all` и `enrich_around`, добавить `enrich_ids`:

```python
def enrich_all(conn: psycopg.Connection) -> int:
    with conn.cursor() as cur:
        cur.execute(_ENRICH_SQL, {"radius": settings.poi_radius_m,
                                  "filter_geog": None, "filter_ids": None})
        n = cur.rowcount
    conn.commit()
    return n


def enrich_around(conn: psycopg.Connection, poi_geom_wkt: str) -> int:
    params = {"radius": settings.poi_radius_m,
              "filter_geog": f"SRID=4326;{poi_geom_wkt}", "filter_ids": None}
    with conn.cursor() as cur:
        cur.execute(_ENRICH_SQL, params)
        n = cur.rowcount
    conn.commit()
    return n


def enrich_ids(conn: psycopg.Connection, external_ids: list[str]) -> int:
    """Обогащает ровно перечисленные объявления.

    Нужен публикации из личного кабинета: enrich_all переписывает всю таблицу
    (на рабочем объёме — минуты), а публикуется одно объявление.
    """
    if not external_ids:
        return 0
    params = {"radius": settings.poi_radius_m,
              "filter_geog": None, "filter_ids": list(external_ids)}
    with conn.cursor() as cur:
        cur.execute(_ENRICH_SQL, params)
        n = cur.rowcount
    conn.commit()
    return n
```

Проверить, что тело `enrich_around` совпадает с оригиналом (`habitus/geo/enrich.py:109-115`)
по всему, кроме добавленного `filter_ids`.

- [ ] **Step 5: Перенести построение `doc_text` в `document.py`**

В `habitus/embed/document.py` добавить в конец файла:

```python
def refresh_doc_text(conn, external_ids: list[str] | None = None) -> int:
    """Пересобирает doc_text. Без списка — по всей таблице (батч-пайплайн),
    со списком — только по указанным объявлениям (публикация из кабинета)."""
    from psycopg.rows import dict_row

    sql = "SELECT * FROM listings"
    params: tuple = ()
    if external_ids is not None:
        if not external_ids:
            return 0
        sql += " WHERE external_id = ANY(%s)"
        params = (list(external_ids),)
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute(sql + ";", params)
        rows = cur.fetchall()
    with conn.cursor() as cur:
        for r in rows:
            cur.execute("UPDATE listings SET doc_text=%s WHERE external_id=%s;",
                        (build_doc_text(r), r["external_id"]))
    conn.commit()
    return len(rows)
```

В `habitus/cli.py` удалить локальную `_refresh_doc_text` (строки 20-29), заменить
импорт и вызов:

```python
from habitus.embed.document import build_doc_text, refresh_doc_text
```

и в `run_offline` вместо `stats["doc_text"] = _refresh_doc_text(conn)` —
`stats["doc_text"] = refresh_doc_text(conn)`. Проверить, что `build_doc_text`
всё ещё используется в `cli.py`; если нет — убрать из импорта.

- [ ] **Step 6: Ограничить `embed_pending` списком id**

В `habitus/embed/encode.py` заменить начало `embed_pending`:

```python
def embed_pending(conn: psycopg.Connection, model=None,
                  external_ids: list[str] | None = None) -> int:
    # берём все строки с doc_text и их сохранённый хэш; изменившиеся — те,
    # у кого hash(doc_text) != content_hash (в т.ч. NULL при первом прогоне).
    # external_ids сужает выборку: при публикации одного объявления из кабинета
    # незачем хэшировать весь корпус.
    sql = "SELECT external_id, doc_text, content_hash FROM listings WHERE doc_text IS NOT NULL"
    params: tuple = ()
    if external_ids is not None:
        if not external_ids:
            return 0
        sql += " AND external_id = ANY(%s)"
        params = (list(external_ids),)
    with conn.cursor() as cur:
        cur.execute(sql + ";", params)
        rows = cur.fetchall()
```

Остальное тело функции не меняется.

- [ ] **Step 7: Убедиться, что тесты проходят**

Запустить: `uv run pytest tests/test_scoped_enrich.py -v`
Ожидается: 3 passed.

- [ ] **Step 8: Прогнать весь Python-набор**

Запустить: `uv run pytest`
Ожидается: 0 failed. Особое внимание — `tests/test_pipeline.py` и
`tests/test_cli_smoke.py`: они дёргают `run_offline` целиком.

- [ ] **Step 9: Коммит**

```bash
git add habitus/geo/enrich.py habitus/embed/document.py habitus/embed/encode.py habitus/cli.py tests/test_scoped_enrich.py
git commit -m "feat: точечное обогащение и эмбеддинг по списку объявлений"
```

---

### Task 5: Ручки `owner-upsert` и `owner-withdraw`

**Files:**
- Modify: `habitus/online/schema.py` (добавить схемы в конец файла)
- Create: `habitus/online/owner_listing.py`
- Modify: `habitus/online/service.py` (две ручки после `/dossier`)
- Test: `tests/test_owner_listing_service.py`

**Interfaces:**
- Consumes: `is_valid`, `CITY_BBOX` (Task 2); `enrich_ids`, `refresh_doc_text`,
  `embed_pending` (Task 4)
- Produces:
  - `habitus.online.schema.OwnerListingUpsertRequest` — поля:
    `external_id: str`, `source: Literal["cian","owner"]`, `city: Literal["msk","spb"]`,
    `price: int | None`, `area: float | None`, `kitchen_area: float | None`,
    `rooms: int | None`, `level: int | None`, `levels: int | None`,
    `address: str = ""`, `lng: float`, `lat: float`,
    `window_orientation: list[str] = []`, `description: str = ""`,
    `photos: list[str] = []`, `source_url: str = ""`
  - `habitus.online.schema.OwnerListingUpsertResponse` — `external_id: str`, `indexed: bool`
  - `habitus.online.schema.OwnerListingWithdrawRequest` — `external_id: str`
  - `habitus.online.schema.OwnerListingWithdrawResponse` — `external_id: str`, `deactivated: bool`
  - `habitus.online.owner_listing.OwnerListingInvalid` — исключение с полем `field: str`
  - `habitus.online.owner_listing.upsert_owner_listing(req, conn, model=None) -> bool`
  - `habitus.online.owner_listing.withdraw_owner_listing(external_id, conn) -> bool`
  - HTTP: `POST /listings/owner-upsert`, `POST /listings/owner-withdraw`
  Их зовёт Task 9 (Go-клиент).

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_owner_listing_service.py`:

```python
import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.owner_listing import (OwnerListingInvalid,
                                          upsert_owner_listing,
                                          withdraw_owner_listing)
from habitus.online.schema import OwnerListingUpsertRequest


class FakeModel:
    """Возвращает векторы нужной размерности, не поднимая BGE-M3."""

    def encode(self, texts, **kwargs):
        return {"dense_vecs": [[0.01] * 1024 for _ in texts],
                "lexical_weights": [{"1": 0.5} for _ in texts]}


def _req(**over) -> OwnerListingUpsertRequest:
    base = dict(external_id="owner_test1", source="owner", city="msk",
                price=12_000_000, area=54.0, kitchen_area=9.0, rooms=2,
                level=4, levels=17, address="Москва, улица Мельникова, 3к1",
                lng=37.6595, lat=55.7108, window_orientation=["юг"],
                description="Тихая двушка окнами во двор", photos=[],
                source_url="")
    base.update(over)
    return OwnerListingUpsertRequest(**base)


def _clean(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings CASCADE;")
    conn.commit()


def test_upsert_creates_indexed_owner_managed_row():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)

        indexed = upsert_owner_listing(_req(), conn, model=FakeModel())

        with conn.cursor() as cur:
            cur.execute("""SELECT source, owner_managed, is_active,
                                  doc_text IS NOT NULL, embedding IS NOT NULL,
                                  ST_X(geom), ST_Y(geom)
                           FROM listings WHERE external_id='owner_test1';""")
            row = cur.fetchone()
    assert indexed is True
    assert row[:5] == ("owner", True, True, True, True)
    assert round(row[5], 4) == 37.6595
    assert round(row[6], 4) == 55.7108


def test_upsert_is_idempotent_and_updates_price():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        upsert_owner_listing(_req(), conn, model=FakeModel())

        upsert_owner_listing(_req(price=11_000_000), conn, model=FakeModel())

        with conn.cursor() as cur:
            cur.execute("SELECT count(*), max(price) FROM listings WHERE external_id='owner_test1';")
            count, price = cur.fetchone()
    assert count == 1
    assert price == 11_000_000


def test_upsert_rejects_coordinates_of_another_city():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        with pytest.raises(OwnerListingInvalid) as exc:
            upsert_owner_listing(_req(city="spb"), conn, model=FakeModel())
    assert exc.value.field == "coordinates"


def test_upsert_rejects_absurd_price():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        with pytest.raises(OwnerListingInvalid) as exc:
            upsert_owner_listing(_req(price=1000), conn, model=FakeModel())
    assert exc.value.field == "price"


def test_withdraw_deactivates_without_deleting():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        upsert_owner_listing(_req(), conn, model=FakeModel())

        deactivated = withdraw_owner_listing("owner_test1", conn)

        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='owner_test1';")
            is_active = cur.fetchone()[0]
    assert deactivated is True
    assert is_active is False


def test_withdraw_unknown_id_is_not_an_error():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        assert withdraw_owner_listing("owner_nope", conn) is False
```

- [ ] **Step 2: Убедиться, что тест падает**

Запустить: `uv run pytest tests/test_owner_listing_service.py -v`
Ожидается: FAIL — `ModuleNotFoundError: No module named 'habitus.online.owner_listing'`.

- [ ] **Step 3: Добавить схемы**

В конец `habitus/online/schema.py`:

```python
# --- Объявление из личного кабинета продавца -------------------------------
# Витрина принимает его той же формой, что и объявление источника: разница
# только в source и в том, что строка помечается owner_managed.

class OwnerListingUpsertRequest(BaseModel):
    external_id: str = Field(min_length=1, max_length=128)
    source: Literal["cian", "owner"]
    city: Literal["msk", "spb"]
    price: int | None = None
    area: float | None = None
    kitchen_area: float | None = None
    rooms: int | None = None
    level: int | None = None
    levels: int | None = None
    address: str = ""
    lng: float
    lat: float
    window_orientation: list[str] = Field(default_factory=list)
    description: str = ""
    photos: list[str] = Field(default_factory=list)
    source_url: str = ""


class OwnerListingUpsertResponse(BaseModel):
    external_id: str
    indexed: bool


class OwnerListingWithdrawRequest(BaseModel):
    external_id: str = Field(min_length=1, max_length=128)


class OwnerListingWithdrawResponse(BaseModel):
    external_id: str
    deactivated: bool
```

Проверить, что `Literal` и `Field` уже импортированы в шапке файла; если нет —
добавить в существующие импорты `from typing import Literal` и
`from pydantic import BaseModel, Field`.

- [ ] **Step 4: Реализовать логику**

Создать `habitus/online/owner_listing.py`:

```python
"""Приём объявления из личного кабинета продавца в витрину.

Отдельный модуль, а не часть ingest/: объявление продавца не приходит обходом
источника и не попадает в raw_listings — эта таблица зеркалит снимок краулера,
а у объявления из кабинета никакого снимка нет.
"""
import psycopg

from habitus.clean.normalize import is_valid
from habitus.embed.document import refresh_doc_text
from habitus.embed.encode import embed_pending
from habitus.geo.enrich import enrich_ids
from habitus.online.schema import OwnerListingUpsertRequest


class OwnerListingInvalid(Exception):
    """Объявление не проходит те же пороги, что и объявление источника."""

    def __init__(self, field: str, message: str):
        super().__init__(message)
        self.field = field


_UPSERT_SQL = """
    INSERT INTO listings
      (external_id, source, price, area, kitchen_area, rooms, level, levels,
       geom, description, city, address, source_url, window_orientation,
       photos, owner_managed, is_active)
    VALUES
      (%(external_id)s, %(source)s, %(price)s, %(area)s, %(kitchen_area)s,
       %(rooms)s, %(level)s, %(levels)s,
       ST_SetSRID(ST_MakePoint(%(lng)s, %(lat)s), 4326), %(description)s,
       %(city)s, %(address)s, %(source_url)s, %(window_orientation)s,
       %(photos)s, true, true)
    ON CONFLICT (external_id) DO UPDATE SET
       price=EXCLUDED.price, area=EXCLUDED.area,
       kitchen_area=EXCLUDED.kitchen_area, rooms=EXCLUDED.rooms,
       level=EXCLUDED.level, levels=EXCLUDED.levels, geom=EXCLUDED.geom,
       description=EXCLUDED.description, city=EXCLUDED.city,
       address=EXCLUDED.address, source_url=EXCLUDED.source_url,
       window_orientation=EXCLUDED.window_orientation, photos=EXCLUDED.photos,
       owner_managed=true, is_active=true, updated_at=now();
"""


def _validate(req: OwnerListingUpsertRequest) -> None:
    row = {"price": req.price, "area": req.area,
           "lat": req.lat, "lon": req.lng, "city": req.city}
    if not (req.price and 1_000_000 <= req.price <= 3_000_000_000):
        raise OwnerListingInvalid("price", "Цена вне диапазона 1 млн — 3 млрд ₽")
    if not (req.area and 5 <= req.area <= 1000):
        raise OwnerListingInvalid("area", "Площадь вне диапазона 5—1000 м²")
    if not is_valid(row):
        raise OwnerListingInvalid("coordinates",
                                  "Координаты вне границ выбранного города")


def upsert_owner_listing(req: OwnerListingUpsertRequest,
                         conn: psycopg.Connection, model=None) -> bool:
    """Кладёт объявление в витрину, обогащает и индексирует его.

    Возвращает True, если объект получил эмбеддинг и, значит, находится
    семантическим поиском. Объект без вектора хуже отсутствующего: он лежит в
    базе и не находится, поэтому результат индексации возвращается наружу.
    """
    _validate(req)
    with conn.cursor() as cur:
        cur.execute(_UPSERT_SQL, {
            "external_id": req.external_id, "source": req.source,
            "price": req.price, "area": req.area, "kitchen_area": req.kitchen_area,
            "rooms": req.rooms, "level": req.level, "levels": req.levels,
            "lng": req.lng, "lat": req.lat, "description": req.description or None,
            "city": req.city, "address": req.address or None,
            "source_url": req.source_url or None,
            "window_orientation": req.window_orientation or None,
            "photos": req.photos or None,
        })
    conn.commit()

    enrich_ids(conn, [req.external_id])
    refresh_doc_text(conn, [req.external_id])
    embed_pending(conn, model=model, external_ids=[req.external_id])

    with conn.cursor() as cur:
        cur.execute("SELECT embedding IS NOT NULL FROM listings WHERE external_id=%s;",
                    (req.external_id,))
        row = cur.fetchone()
    return bool(row and row[0])


def withdraw_owner_listing(external_id: str, conn: psycopg.Connection) -> bool:
    """Снимает объявление с публикации. Строку не удаляет: повторная
    публикация должна оживить объект вместе с уже посчитанным эмбеддингом."""
    with conn.cursor() as cur:
        cur.execute("""UPDATE listings SET is_active=false, updated_at=now()
                       WHERE external_id=%s AND owner_managed;""", (external_id,))
        affected = cur.rowcount
    conn.commit()
    return affected > 0
```

- [ ] **Step 5: Поднять ручки в FastAPI**

В `habitus/online/service.py` добавить в импорт схем
`OwnerListingUpsertRequest, OwnerListingUpsertResponse, OwnerListingWithdrawRequest,
OwnerListingWithdrawResponse` и после эндпоинта `/dossier`:

```python
@app.post("/listings/owner-upsert", response_model=OwnerListingUpsertResponse)
def owner_upsert(req: OwnerListingUpsertRequest) -> OwnerListingUpsertResponse:
    from habitus.online.owner_listing import (OwnerListingInvalid,
                                              upsert_owner_listing)
    try:
        with get_conn() as conn:
            indexed = upsert_owner_listing(req, conn)
    except OwnerListingInvalid as exc:
        # 422 с именем поля: шлюз показывает продавцу, что именно поправить.
        raise HTTPException(status_code=422,
                            detail={"field": exc.field, "message": str(exc)}) from exc
    return OwnerListingUpsertResponse(external_id=req.external_id, indexed=indexed)


@app.post("/listings/owner-withdraw", response_model=OwnerListingWithdrawResponse)
def owner_withdraw(req: OwnerListingWithdrawRequest) -> OwnerListingWithdrawResponse:
    from habitus.online.owner_listing import withdraw_owner_listing
    with get_conn() as conn:
        deactivated = withdraw_owner_listing(req.external_id, conn)
    return OwnerListingWithdrawResponse(external_id=req.external_id,
                                        deactivated=deactivated)
```

- [ ] **Step 6: Убедиться, что тесты проходят**

Запустить: `uv run pytest tests/test_owner_listing_service.py -v`
Ожидается: 6 passed.

- [ ] **Step 7: Проверить, что схемы не сломали контрактный тест**

Запустить: `uv run pytest tests/test_online_schema.py tests/test_service.py -v`
Ожидается: 0 failed.

- [ ] **Step 8: Прогнать весь Python-набор**

Запустить: `uv run pytest`
Ожидается: 0 failed.

- [ ] **Step 9: Коммит**

```bash
git add habitus/online/schema.py habitus/online/owner_listing.py habitus/online/service.py tests/test_owner_listing_service.py
git commit -m "feat: ML-ручки приёма и снятия объявления продавца"
```

---

## Слой 3 — Go: забор одного объявления с Циана

### Task 6: Разбор ссылки на объявление

**Files:**
- Create: `backend/internal/cian/offerurl.go`
- Test: `backend/internal/cian/offerurl_test.go`

**Interfaces:**
- Consumes: ничего
- Produces: `cian.ParseOfferURL(raw string) (int64, error)` и
  `cian.ErrNotAnOfferURL`. Использует Task 10.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/cian/offerurl_test.go`:

```go
package cian

import (
	"errors"
	"testing"
)

func TestParseOfferURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"канонический sale", "https://www.cian.ru/sale/flat/318394906/", 318394906},
		{"хвост сессии", "https://www.cian.ru/sale/flat/317927888/?mlSearchSessionGuid=c532b9c1", 317927888},
		{"аренда", "https://www.cian.ru/rent/flat/302010101/", 302010101},
		{"форма deal", "https://www.cian.ru/deal/sale/flat/318394906/", 318394906},
		{"поддомен города", "https://spb.cian.ru/sale/flat/311111111/", 311111111},
		{"мобильный поддомен", "https://m.cian.ru/sale/flat/311111112/", 311111112},
		{"без схемы", "www.cian.ru/sale/flat/318394906/", 318394906},
		{"без www", "https://cian.ru/sale/flat/318394906", 318394906},
		{"голый id", "318394906", 318394906},
		{"пробелы вокруг", "  https://www.cian.ru/sale/flat/318394906/  ", 318394906},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOfferURL(tc.in)
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if got != tc.want {
				t.Fatalf("получено %d, ожидалось %d", got, tc.want)
			}
		})
	}
}

func TestParseOfferURLRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"пусто", ""},
		{"чужой домен", "https://www.avito.ru/moskva/kvartiry/1234567"},
		{"домен-подделка", "https://cian.ru.evil.example/sale/flat/318394906/"},
		{"страница поиска", "https://www.cian.ru/cat.php?deal_type=sale"},
		{"жилой комплекс", "https://www.cian.ru/zhk/shift-12345/"},
		{"не число", "https://www.cian.ru/sale/flat/abcdef/"},
		{"просто текст", "моя квартира"},
		{"id нулевой", "0"},
		{"id слишком длинный", "12345678901234567890123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseOfferURL(tc.in); !errors.Is(err, ErrNotAnOfferURL) {
				t.Fatalf("ожидался ErrNotAnOfferURL, получено %v", err)
			}
		})
	}
}
```

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/cian/ -run TestParseOfferURL -v`
Ожидается: FAIL — `undefined: ParseOfferURL`.

- [ ] **Step 3: Реализовать**

Создать `backend/internal/cian/offerurl.go`:

```go
package cian

import (
	"errors"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
)

// ErrNotAnOfferURL — вход не похож на ссылку на объявление Циана.
// Сервис превращает его в 400 cian_url_invalid с человеческим текстом:
// продавец вставляет ссылку из адресной строки, и «invalid request body»
// ему ничего не объясняет.
var ErrNotAnOfferURL = errors.New("not a Cian offer URL")

// Хост проверяется отдельно от пути, а не одной регуляркой по всей строке:
// иначе `cian.ru.evil.example/sale/flat/1/` прошло бы как валидное.
var (
	offerHostRe = regexp.MustCompile(`^(?:https?://)?(?:[a-z0-9-]+\.)*cian\.ru$`)
	offerPathRe = regexp.MustCompile(`^/(?:deal/)?(?:sale|rent)/flat/(\d{1,15})/?$`)
	bareIDRe    = regexp.MustCompile(`^\d{1,15}$`)
)

// ParseOfferURL достаёт числовой id объявления из того, что продавец вставил
// в поле: полной ссылки в любой форме или голого id.
func ParseOfferURL(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, ErrNotAnOfferURL
	}

	if bareIDRe.MatchString(value) {
		return parsePositive(value)
	}

	withScheme := value
	if !strings.HasPrefix(withScheme, "http://") && !strings.HasPrefix(withScheme, "https://") {
		withScheme = "https://" + withScheme
	}
	parsed, err := neturl.Parse(withScheme)
	if err != nil {
		return 0, ErrNotAnOfferURL
	}
	if !offerHostRe.MatchString(strings.ToLower(parsed.Host)) {
		return 0, ErrNotAnOfferURL
	}
	match := offerPathRe.FindStringSubmatch(parsed.Path)
	if match == nil {
		return 0, ErrNotAnOfferURL
	}
	return parsePositive(match[1])
}

func parsePositive(digits string) (int64, error) {
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrNotAnOfferURL
	}
	return id, nil
}
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/cian/ -run TestParseOfferURL -v`
Ожидается: 19 подтестов passed.

- [ ] **Step 5: Коммит**

```bash
git add backend/internal/cian/offerurl.go backend/internal/cian/offerurl_test.go
git commit -m "feat: разбор ссылки на объявление Циана в числовой id"
```

---

### Task 7: Забор одного объявления по id

**Files:**
- Modify: `backend/internal/cian/client.go:200-227` (`BuildSearchBody`), добавить `FetchByID`
- Create: `backend/internal/cian/fixtures_test.go` (зафиксированный ответ API)
- Test: `backend/internal/cian/fetchbyid_test.go`

**Interfaces:**
- Consumes: `Session`, `ParseSearchResponse`, `apiHeaders`, `session.do`, `ErrBlocked`
- Produces:
  - `cian.BuildOfferBody(region int, offerID int64) ([]byte, error)`
  - `(*Session).FetchByID(ctx context.Context, offerID int64) (Listing, error)`
  - `cian.ErrOfferNotFound`
  Использует Task 10.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/cian/fetchbyid_test.go`:

```go
package cian

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestBuildOfferBodyFiltersByID(t *testing.T) {
	body, err := BuildOfferBody(1, 318394906)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var decoded struct {
		JSONQuery struct {
			Type string `json:"_type"`
			IDs  struct {
				Type  string  `json:"type"`
				Value []int64 `json:"value"`
			} `json:"ids"`
			Region struct {
				Value []int `json:"value"`
			} `json:"region"`
		} `json:"jsonQuery"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("тело запроса не разбирается: %v", err)
	}
	if decoded.JSONQuery.Type != "flatsale" {
		t.Fatalf("_type = %q", decoded.JSONQuery.Type)
	}
	if decoded.JSONQuery.IDs.Type != "terms" || len(decoded.JSONQuery.IDs.Value) != 1 ||
		decoded.JSONQuery.IDs.Value[0] != 318394906 {
		t.Fatalf("фильтр ids собран неверно: %+v", decoded.JSONQuery.IDs)
	}
	if len(decoded.JSONQuery.Region.Value) != 1 || decoded.JSONQuery.Region.Value[0] != 1 {
		t.Fatalf("регион собран неверно: %+v", decoded.JSONQuery.Region)
	}
}

func TestBuildOfferBodyRejectsBadInput(t *testing.T) {
	if _, err := BuildOfferBody(1, 0); err == nil {
		t.Fatal("нулевой id должен отвергаться")
	}
	if _, err := BuildOfferBody(0, 123); err == nil {
		t.Fatal("нулевой регион должен отвергаться")
	}
}

func TestFetchByIDParsesOffer(t *testing.T) {
	session := newSessionForTest(&stubDoer{
		body:        []byte(offerResponseJSON),
		contentType: "application/json",
		status:      200,
	}, SessionConfig{})

	listing, err := session.FetchByID(context.Background(), 318394906)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if listing.CianID != "318394906" {
		t.Fatalf("cian_id = %q", listing.CianID)
	}
	if listing.Price == nil || *listing.Price != 45007350 {
		t.Fatalf("price = %v", listing.Price)
	}
	if listing.Latitude == nil || listing.Longitude == nil {
		t.Fatal("координаты обязаны быть разобраны")
	}
	if listing.CollectedAt.IsZero() {
		t.Fatal("collected_at должен быть проставлен")
	}
	if time.Since(listing.CollectedAt) > time.Minute {
		t.Fatal("collected_at должен быть моментом запроса")
	}
}

func TestFetchByIDNotFoundOnEmptyList(t *testing.T) {
	session := newSessionForTest(&stubDoer{
		body:        []byte(`{"data":{"offersSerialized":[]}}`),
		contentType: "application/json",
		status:      200,
	}, SessionConfig{})

	if _, err := session.FetchByID(context.Background(), 1); !errors.Is(err, ErrOfferNotFound) {
		t.Fatalf("ожидался ErrOfferNotFound, получено %v", err)
	}
}

func TestFetchByIDReportsBlock(t *testing.T) {
	session := newSessionForTest(&stubDoer{
		body:        []byte(`<html><title>Captcha</title></html>`),
		contentType: "text/html",
		status:      200,
	}, SessionConfig{})

	if _, err := session.FetchByID(context.Background(), 1); !errors.Is(err, ErrBlocked) {
		t.Fatalf("ожидался ErrBlocked, получено %v", err)
	}
}
```

- [ ] **Step 2: Подготовить фикстуру и стаб транспорта**

Открыть `backend/internal/cian/client_test.go` и `backend/internal/cian/parser_test.go`,
найти уже существующий стаб `httpDoer` и уже существующую фикстуру ответа API.
**Переиспользовать их**, а не дублировать: если стаб называется иначе, чем
`stubDoer`, — поправить имя в тесте Task 7 под существующее и не создавать
`fixtures_test.go`. Новый файл фикстуры создавать только если готовой нет.

Если готовой фикстуры нет, создать `backend/internal/cian/fixtures_test.go`:

```go
package cian

// offerResponseJSON — усечённый до нужных полей реальный ответ
// api.cian.ru/search-offers/v2/search-offers-desktop/ на запрос с фильтром ids.
// Живого Циана в тестах нет: он отдаёт 403 из CI и капчу под нагрузкой.
const offerResponseJSON = `{
  "data": {
    "offersSerialized": [
      {
        "id": 318394906,
        "bargainTerms": {"priceRur": 45007350},
        "totalArea": "46.5",
        "roomsCount": 1,
        "floorNumber": 2,
        "building": {"floorsCount": 18, "materialType": "monolith"},
        "geo": {
          "userInput": "Москва, 2-й Донской проезд",
          "coordinates": {"lat": 55.71120458532715, "lng": 37.592330829357934},
          "undergrounds": [
            {"name": "Ленинский проспект", "time": 7, "transportType": "walk"}
          ]
        },
        "photos": [{"fullUrl": "https://images.cdn-cian.ru/images/2943545902-1.jpg"}],
        "description": "1-к квартира в премиальном комплексе SHIFT",
        "fullUrl": "https://www.cian.ru/sale/flat/318394906/"
      }
    ]
  }
}`
```

**Важно:** поля фикстуры должны совпадать с путями, которые читает
`parseOffer` (`backend/internal/cian/parser.go:47`). Перед написанием фикстуры
прочитать `parseOffer`, `parseAddress`, `parsePhotos`, `parseMetro` и
`ParseSearchResponse` и выстроить структуру под них — иначе тест будет проверять
выдуманный формат.

- [ ] **Step 3: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/cian/ -run 'TestBuildOfferBody|TestFetchByID' -v`
Ожидается: FAIL — `undefined: BuildOfferBody`, `undefined: ErrOfferNotFound`.

- [ ] **Step 4: Реализовать**

В `backend/internal/cian/client.go` добавить сентинел рядом с `ErrBlocked`:

```go
// ErrOfferNotFound — Циан ответил пустым списком: объявление снято, скрыто
// или id не существует. Отличать от ErrBlocked обязательно: первое — сообщение
// продавцу, второе — повод сменить сессию и прокси.
var ErrOfferNotFound = errors.New("Cian offer not found")
```

Добавить сборку тела запроса рядом с `BuildSearchBody`:

```go
// BuildOfferBody — тот же внутренний запрос Циана, но суженный до одного
// объявления фильтром ids. Отдельная функция, а не флаг в Filter: у забора по
// id нет ни страниц, ни ценовых окон, и смешивать их в одной сигнатуре значит
// плодить невозможные комбинации.
func BuildOfferBody(region int, offerID int64) ([]byte, error) {
	if region < 1 {
		return nil, errors.New("region must be at least 1")
	}
	if offerID < 1 {
		return nil, errors.New("offer id must be at least 1")
	}
	query := map[string]any{
		"region":         map[string]any{"type": "terms", "value": []int{region}},
		"_type":          "flatsale",
		"engine_version": map[string]any{"type": "term", "value": 2},
		"ids":            map[string]any{"type": "terms", "value": []int64{offerID}},
	}
	return json.Marshal(map[string]any{"jsonQuery": query})
}
```

Добавить метод рядом с `Search`:

```go
// FetchByID забирает одно объявление. В отличие от Search, вызывается
// интерактивно — продавец ждёт ответа, — поэтому пауз между запросами здесь
// нет: темп ограничивает вызывающая сторона общим лимитером.
func (session *Session) FetchByID(ctx context.Context, offerID int64) (Listing, error) {
	if session.config.BootstrapCookies && !session.initialized {
		if err := session.bootstrap(ctx); err != nil {
			return Listing{}, err
		}
	}

	body, err := BuildOfferBody(session.config.Region, offerID)
	if err != nil {
		return Listing{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, session.config.APIURL, bytes.NewReader(body))
	if err != nil {
		return Listing{}, fmt.Errorf("create Cian API request: %w", err)
	}
	req.Header = apiHeaders(session.identity)

	responseBody, contentType, err := session.do(req)
	if err != nil {
		return Listing{}, err
	}
	if isBlockedAPIResponse(contentType, responseBody) {
		return Listing{}, ErrBlocked
	}
	if !json.Valid(responseBody) {
		return Listing{}, fmt.Errorf("Cian API returned non-JSON response (%s)", contentType)
	}
	offers, err := ParseSearchResponse(responseBody, time.Now())
	if err != nil {
		return Listing{}, err
	}
	if len(offers) == 0 {
		return Listing{}, ErrOfferNotFound
	}
	return offers[0], nil
}
```

- [ ] **Step 5: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/cian/ -v`
Ожидается: 0 failed, включая уже существовавшие тесты пакета.

- [ ] **Step 6: Коммит**

```bash
git add backend/internal/cian/
git commit -m "feat: забор одного объявления Циана по id"
```

---

### Task 8: Лимитеры запросов к Циану

**Files:**
- Create: `backend/internal/cian/limiter.go`
- Test: `backend/internal/cian/limiter_test.go`

**Interfaces:**
- Consumes: ничего
- Produces:
  - `cian.NewRateLimiter(perMinute int, now func() time.Time) *RateLimiter`
    с методом `Allow() bool` — глобальный потолок исходящих запросов
  - `cian.NewUserQuota(perHour int, now func() time.Time) *UserQuota`
    с методом `Allow(userID string) bool` — квота на пользователя
  Оба использует Task 10. Инъекция `now` обязательна: тесты не должны спать.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/cian/limiter_test.go`:

```go
package cian

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterCapsBurst(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(3, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if !limiter.Allow() {
			t.Fatalf("запрос %d должен был пройти", i+1)
		}
	}
	if limiter.Allow() {
		t.Fatal("четвёртый запрос за ту же минуту должен быть отклонён")
	}
}

func TestRateLimiterRecoversAfterWindow(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(1, func() time.Time { return now })

	if !limiter.Allow() {
		t.Fatal("первый запрос должен пройти")
	}
	if limiter.Allow() {
		t.Fatal("второй запрос в том же окне должен быть отклонён")
	}
	now = now.Add(61 * time.Second)
	if !limiter.Allow() {
		t.Fatal("после окна лимит должен восстановиться")
	}
}

func TestRateLimiterIsConcurrencySafe(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(50, func() time.Time { return now })

	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow() {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Fatalf("пропущено %d запросов вместо 50", allowed)
	}
}

func TestUserQuotaIsPerUser(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	quota := NewUserQuota(2, func() time.Time { return now })

	if !quota.Allow("alice") || !quota.Allow("alice") {
		t.Fatal("две попытки alice должны пройти")
	}
	if quota.Allow("alice") {
		t.Fatal("третья попытка alice должна быть отклонена")
	}
	if !quota.Allow("bob") {
		t.Fatal("квота bob не должна зависеть от alice")
	}
}

func TestUserQuotaWindowSlides(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	quota := NewUserQuota(1, func() time.Time { return now })

	if !quota.Allow("alice") {
		t.Fatal("первая попытка должна пройти")
	}
	now = now.Add(59 * time.Minute)
	if quota.Allow("alice") {
		t.Fatal("внутри часа вторая попытка должна быть отклонена")
	}
	now = now.Add(2 * time.Minute)
	if !quota.Allow("alice") {
		t.Fatal("за пределами часа лимит должен восстановиться")
	}
}
```

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/cian/ -run 'TestRateLimiter|TestUserQuota' -v`
Ожидается: FAIL — `undefined: NewRateLimiter`.

- [ ] **Step 3: Реализовать**

Создать `backend/internal/cian/limiter.go`:

```go
package cian

import (
	"sync"
	"time"
)

// RateLimiter — общий потолок исходящих запросов к Циану, а не лимит на
// пользователя. Сколько бы человек ни импортировали одновременно, наружу
// уходит не больше perMinute запросов: бан прилетает по IP всему сервису
// сразу, поэтому ограничивать надо суммарный темп.
//
// Окно скользящее, отметки хранятся списком: perMinute — единицы, поэтому
// цена обхода списка ничтожна, а поведение точнее, чем у ведра с доливом.
type RateLimiter struct {
	mu        sync.Mutex
	perMinute int
	now       func() time.Time
	marks     []time.Time
}

func NewRateLimiter(perMinute int, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{perMinute: perMinute, now: now}
}

func (l *RateLimiter) Allow() bool {
	if l.perMinute <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-time.Minute)
	fresh := l.marks[:0]
	for _, m := range l.marks {
		if m.After(cutoff) {
			fresh = append(fresh, m)
		}
	}
	l.marks = fresh
	if len(l.marks) >= l.perMinute {
		return false
	}
	l.marks = append(l.marks, l.now())
	return true
}

// UserQuota — скользящее часовое окно на пользователя. Защищает не Циан, а
// сервис: один человек не должен выбирать общий потолок целиком.
type UserQuota struct {
	mu      sync.Mutex
	perHour int
	now     func() time.Time
	marks   map[string][]time.Time
}

func NewUserQuota(perHour int, now func() time.Time) *UserQuota {
	if now == nil {
		now = time.Now
	}
	return &UserQuota{perHour: perHour, now: now, marks: map[string][]time.Time{}}
}

func (q *UserQuota) Allow(userID string) bool {
	if q.perHour <= 0 {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := q.now().Add(-time.Hour)
	kept := q.marks[userID][:0]
	for _, m := range q.marks[userID] {
		if m.After(cutoff) {
			kept = append(kept, m)
		}
	}
	if len(kept) >= q.perHour {
		// Отметки чистим даже при отказе: иначе список растёт бесконечно.
		q.marks[userID] = kept
		return false
	}
	q.marks[userID] = append(kept, q.now())
	return true
}
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/cian/ -race -v`
Ожидается: 0 failed. Флаг `-race` обязателен: лимитер разделяется между
горутинами обработчиков.

- [ ] **Step 5: Коммит**

```bash
git add backend/internal/cian/limiter.go backend/internal/cian/limiter_test.go
git commit -m "feat: общий и пользовательский лимитеры запросов к Циану"
```

---

## Слой 4 — Go: сервис кабинета и ручки шлюза

### Task 9: Коды ошибок, ML-клиент и настройки

**Files:**
- Modify: `backend/internal/apperr/apperr.go` (добавить фабрики в конец)
- Modify: `backend/internal/client/ml_client.go` (DTO и два метода)
- Modify: `backend/internal/config/config.go` (новые поля `Settings`)
- Modify: `.env.example`
- Test: `backend/internal/client/ml_owner_test.go`

**Interfaces:**
- Consumes: `postJSON`, `ErrServer`, `ErrBadResponse`, `ErrTimeout` из `ml_client.go`
- Produces:
  - `apperr.CianURLInvalid()`, `apperr.CianOfferNotFound()`, `apperr.CianUnavailable()`,
    `apperr.ListingClaimedByOther()`, `apperr.OwnerListingNotFound()`,
    `apperr.PhotoTooLarge(maxMB int)`, `apperr.PhotoUnsupportedFormat()`,
    `apperr.PhotoLimitExceeded(max int)`, `apperr.OwnerListingInvalid(field, message string)`
  - `client.OwnerUpsertRequest`, `client.OwnerUpsertResponse`,
    `client.OwnerWithdrawResponse`, `client.ErrOwnerListingInvalid`
  - `(*MLClient).OwnerUpsert(ctx, req) (*OwnerUpsertResponse, error)`
  - `(*MLClient).OwnerWithdraw(ctx, externalID string) (*OwnerWithdrawResponse, error)`
  - `config.Settings` поля `CianFetchPerMin`, `OwnerImportPerHour`,
    `OwnerAutopublish`, `MLOwnerTimeoutS`, `OwnerPhotoMaxMB`, `OwnerPhotoMaxCount`,
    `CianProxies []string`, `CianRegion int`
  Всё это потребляют Task 10, 11, 12.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/client/ml_owner_test.go`:

```go
package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnerUpsertSendsPayloadAndReadsIndexed(t *testing.T) {
	var got OwnerUpsertRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/owner-upsert" {
			t.Errorf("путь = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("тело не разбирается: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"external_id":"owner_x","indexed":true}`))
	}))
	defer server.Close()

	c := NewMLClient(server.URL, 5*time.Second)
	price := int64(12_000_000)
	resp, err := c.OwnerUpsert(context.Background(), OwnerUpsertRequest{
		ExternalID: "owner_x", Source: "owner", City: "msk",
		Price: &price, Lng: 37.6, Lat: 55.7,
	})
	if err != nil {
		t.Fatalf("owner upsert: %v", err)
	}
	if !resp.Indexed {
		t.Fatal("indexed должен быть true")
	}
	if got.ExternalID != "owner_x" || got.City != "msk" || got.Lng != 37.6 {
		t.Fatalf("на ML ушло не то: %+v", got)
	}
}

func TestOwnerUpsertMapsValidationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":{"field":"coordinates","message":"Координаты вне границ выбранного города"}}`))
	}))
	defer server.Close()

	c := NewMLClient(server.URL, 5*time.Second)
	_, err := c.OwnerUpsert(context.Background(), OwnerUpsertRequest{ExternalID: "owner_x", Source: "owner", City: "spb"})

	var invalid *OwnerListingInvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
	if invalid.Field != "coordinates" {
		t.Fatalf("поле = %q", invalid.Field)
	}
	if invalid.Message == "" {
		t.Fatal("сообщение должно доезжать до продавца")
	}
}

func TestOwnerWithdraw(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/owner-withdraw" {
			t.Errorf("путь = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"external_id":"owner_x","deactivated":true}`))
	}))
	defer server.Close()

	c := NewMLClient(server.URL, 5*time.Second)
	resp, err := c.OwnerWithdraw(context.Background(), "owner_x")
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if !resp.Deactivated {
		t.Fatal("deactivated должен быть true")
	}
}
```

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/client/ -run TestOwner -v`
Ожидается: FAIL — `undefined: OwnerUpsertRequest`.

- [ ] **Step 3: Добавить DTO и методы ML-клиента**

В `backend/internal/client/ml_client.go` добавить рядом с `DossierRequest`:

```go
type OwnerUpsertRequest struct {
	ExternalID        string   `json:"external_id"`
	Source            string   `json:"source"`
	City              string   `json:"city"`
	Price             *int64   `json:"price"`
	Area              *float32 `json:"area"`
	KitchenArea       *float32 `json:"kitchen_area"`
	Rooms             *int     `json:"rooms"`
	Level             *int     `json:"level"`
	Levels            *int     `json:"levels"`
	Address           string   `json:"address"`
	Lng               float64  `json:"lng"`
	Lat               float64  `json:"lat"`
	WindowOrientation []string `json:"window_orientation"`
	Description       string   `json:"description"`
	Photos            []string `json:"photos"`
	SourceURL         string   `json:"source_url"`
}

type OwnerUpsertResponse struct {
	ExternalID string `json:"external_id"`
	Indexed    bool   `json:"indexed"`
}

type OwnerWithdrawResponse struct {
	ExternalID  string `json:"external_id"`
	Deactivated bool   `json:"deactivated"`
}

// OwnerListingInvalidError — 422 от ML: объявление не прошло пороги витрины.
// Отдельный тип, а не ErrBadResponse: продавцу нужно показать, какое поле
// поправить, и без имени поля сообщение бесполезно.
type OwnerListingInvalidError struct {
	Field   string
	Message string
}

func (e *OwnerListingInvalidError) Error() string {
	return e.Field + ": " + e.Message
}
```

Добавить методы рядом с `Dossier`:

```go
func (c *MLClient) OwnerUpsert(ctx context.Context, req OwnerUpsertRequest) (*OwnerUpsertResponse, error) {
	var out OwnerUpsertResponse
	if err := c.postJSONWithValidation(ctx, "/listings/owner-upsert", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *MLClient) OwnerWithdraw(ctx context.Context, externalID string) (*OwnerWithdrawResponse, error) {
	var out OwnerWithdrawResponse
	if err := c.postJSON(ctx, "/listings/owner-withdraw",
		map[string]string{"external_id": externalID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// postJSONWithValidation отличается от postJSON одним: 422 разбирается в
// OwnerListingInvalidError вместо того, чтобы схлопнуться в ErrBadResponse.
func (c *MLClient) postJSONWithValidation(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%w: encode request: %v", ErrBadResponse, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrTimeout
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnprocessableEntity {
		var detail struct {
			Detail struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"detail"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
			return fmt.Errorf("%w: decode 422: %v", ErrBadResponse, err)
		}
		return &OwnerListingInvalidError{Field: detail.Detail.Field, Message: detail.Detail.Message}
	}
	if resp.StatusCode >= 500 {
		return ErrServer
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d", ErrBadResponse, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrBadResponse, err)
	}
	return nil
}
```

- [ ] **Step 4: Добавить коды ошибок**

В конец `backend/internal/apperr/apperr.go`:

```go
func CianURLInvalid() *Error {
	return New(http.StatusBadRequest, "cian_url_invalid",
		"Это не похоже на ссылку на объявление Циана. Скопируйте адрес страницы объявления целиком")
}

func CianOfferNotFound() *Error {
	return New(http.StatusNotFound, "cian_offer_not_found",
		"Циан не отдал такое объявление — возможно, оно снято с публикации")
}

func CianUnavailable() *Error {
	return New(http.StatusServiceUnavailable, "cian_unavailable",
		"Циан сейчас не отдаёт данные. Попробуйте позже или заполните карточку вручную")
}

func ListingClaimedByOther() *Error {
	return New(http.StatusConflict, "listing_claimed_by_other",
		"Это объявление уже привязано к другому аккаунту")
}

func OwnerListingNotFound() *Error {
	return New(http.StatusNotFound, "owner_listing_not_found", "Объявление не найдено")
}

func OwnerListingInvalid(field, message string) *Error {
	return New(http.StatusBadRequest, "owner_listing_invalid", message+" (поле: "+field+")")
}

func PhotoTooLarge(maxMB int) *Error {
	return New(http.StatusBadRequest, "photo_too_large",
		fmt.Sprintf("Фотография больше %d МБ", maxMB))
}

func PhotoUnsupportedFormat() *Error {
	return New(http.StatusBadRequest, "photo_unsupported_format",
		"Поддерживаются только JPEG, PNG и WebP")
}

func PhotoLimitExceeded(max int) *Error {
	return New(http.StatusBadRequest, "photo_limit_exceeded",
		fmt.Sprintf("К объявлению можно приложить не больше %d фотографий", max))
}
```

Добавить `"fmt"` в импорты файла.

- [ ] **Step 5: Добавить настройки**

В `backend/internal/config/config.go` в структуру `Settings`:

```go
	// MLOwnerTimeoutS — публикация объявления продавца: ML считает эмбеддинг
	// BGE-M3, на холодной модели это заметно дольше остальных ручек.
	MLOwnerTimeoutS int
	// CianFetchPerMin — общий потолок исходящих запросов к Циану. Бан прилетает
	// по IP всему сервису сразу, поэтому лимит суммарный, а не на пользователя.
	CianFetchPerMin int
	// OwnerImportPerHour — сколько импортов в час доступно одному продавцу.
	OwnerImportPerHour int
	// OwnerAutopublish — публиковать импортированное объявление сразу.
	// Рубильник на случай наплыва чужих ссылок: false оставляет всё в draft.
	OwnerAutopublish   bool
	OwnerPhotoMaxMB    int
	OwnerPhotoMaxCount int
	// CianProxies — пул прокси для импорта; та же переменная, что у батч-парсера.
	CianProxies []string
	CianRegion  int
```

В `Load()`:

```go
		MLOwnerTimeoutS:    getenvInt("ML_OWNER_TIMEOUT_S", 60),
		CianFetchPerMin:    getenvInt("CIAN_FETCH_PER_MIN", 6),
		OwnerImportPerHour: getenvInt("OWNER_IMPORT_PER_HOUR", 20),
		OwnerAutopublish:   getenvBool("OWNER_AUTOPUBLISH", true),
		OwnerPhotoMaxMB:    getenvInt("OWNER_PHOTO_MAX_MB", 10),
		OwnerPhotoMaxCount: getenvInt("OWNER_PHOTO_MAX_COUNT", 20),
		CianProxies:        getenvList("CIAN_PROXIES"),
		CianRegion:         getenvInt("CIAN_REGION", 1),
```

И вспомогательную функцию рядом с `getenvBool`:

```go
// getenvList читает список, разделённый запятыми или переводами строк, —
// тот же формат, что понимает батч-парсер (cmd/cian-parser/main.go).
func getenvList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' '
	}) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
```

Добавить `"strings"` в импорты.

- [ ] **Step 6: Дописать `.env.example`**

Добавить в `.env.example`:

```
# --- Личный кабинет продавца ---
ML_OWNER_TIMEOUT_S=60
CIAN_FETCH_PER_MIN=6
OWNER_IMPORT_PER_HOUR=20
OWNER_AUTOPUBLISH=true
OWNER_PHOTO_MAX_MB=10
OWNER_PHOTO_MAX_COUNT=20
CIAN_REGION=1
# Прокси для импорта объявлений; без них Циан отдаёт 403 и капчу.
# Формат: http://user:pass@host:port или socks5://host:port, через запятую.
CIAN_PROXIES=
```

- [ ] **Step 7: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/client/ ./internal/config/ ./internal/apperr/ -v`
Ожидается: 0 failed.

- [ ] **Step 8: Коммит**

```bash
git add backend/internal/client/ backend/internal/apperr/ backend/internal/config/ .env.example
git commit -m "feat: ML-клиент объявлений продавца, коды ошибок и настройки кабинета"
```

---

### Task 10: Чтение витрины — привязка и поиск похожих

**Files:**
- Modify: `backend/internal/repository/listing_repo.go` (два метода)
- Test: `backend/internal/repository/listing_owner_test.go`

**Interfaces:**
- Consumes: `ErrNotFound`, `testPool`
- Produces:
  - `domain.ListingSnapshot` — плоский снимок строки `listings` для предпросмотра:
    `ExternalID, Source string; City string; Price *int64; Area *float32;
     KitchenArea *float32; Rooms, Level, Levels *int; Address, Description string;
     Lng, Lat float64; Photos, WindowOrientation []string; SourceURL string;
     OwnerManaged bool`
  - `domain.SimilarListing` — `ExternalID, Address string; Price *int64; Area *float32`
  - `(*ListingRepo).SnapshotByExternalID(ctx, externalID string) (domain.ListingSnapshot, error)`
  - `(*ListingRepo).FindSimilar(ctx, lng, lat float64, rooms, level *int, area *float32, excludeExternalID string) ([]domain.SimilarListing, error)`
  Использует Task 11.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/repository/listing_owner_test.go`:

```go
package repository

import (
	"context"
	"errors"
	"testing"
)

func TestSnapshotByExternalID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE listings CASCADE;`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO listings (external_id, source, price, area, rooms, level, levels,
		                      geom, city, address, description, photos, owner_managed)
		VALUES ('cian_900001', 'cian', 12500000, 54.3, 2, 4, 17,
		        ST_SetSRID(ST_MakePoint(37.6595, 55.7108), 4326), 'msk',
		        'Москва, улица Мельникова, 3к1', 'Тихая двушка',
		        ARRAY['https://images.cdn-cian.ru/1.jpg'], false);`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	repo := NewListingRepo(pool)
	got, err := repo.SnapshotByExternalID(ctx, "cian_900001")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got.Address != "Москва, улица Мельникова, 3к1" || *got.Rooms != 2 {
		t.Fatalf("неверный снимок: %+v", got)
	}
	if got.Lng < 37.65 || got.Lng > 37.67 || got.Lat < 55.70 || got.Lat > 55.72 {
		t.Fatalf("координаты разобраны неверно: %f %f", got.Lng, got.Lat)
	}
	if len(got.Photos) != 1 {
		t.Fatalf("фото не доехали: %+v", got.Photos)
	}
}

func TestSnapshotByExternalIDMissing(t *testing.T) {
	pool := testPool(t)
	repo := NewListingRepo(pool)
	if _, err := repo.SnapshotByExternalID(context.Background(), "cian_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}
}

func TestFindSimilarMatchesNearbyTwin(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE listings CASCADE;`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Близнец в 60 м, чужая квартира в том же доме и далёкая копия.
	if _, err := pool.Exec(ctx, `
		INSERT INTO listings (external_id, source, price, area, rooms, level, geom, city, address)
		VALUES ('cian_twin',    'cian', 12000000, 54.0, 2, 4,
		        ST_SetSRID(ST_MakePoint(37.66046, 55.7108), 4326), 'msk', 'Мельникова 3к1'),
		       ('cian_other',   'cian', 20000000, 88.0, 4, 9,
		        ST_SetSRID(ST_MakePoint(37.6595, 55.7108), 4326), 'msk', 'Мельникова 3к1'),
		       ('cian_faraway', 'cian', 12000000, 54.0, 2, 4,
		        ST_SetSRID(ST_MakePoint(37.50, 55.80), 4326), 'msk', 'Другой район');`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	repo := NewListingRepo(pool)
	rooms, level := 2, 4
	area := float32(54.3)
	found, err := repo.FindSimilar(ctx, 37.6595, 55.7108, &rooms, &level, &area, "cian_new")
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}
	if len(found) != 1 || found[0].ExternalID != "cian_twin" {
		t.Fatalf("ожидался ровно близнец, получено %+v", found)
	}
}

func TestFindSimilarExcludesSelf(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE listings CASCADE;`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO listings (external_id, source, price, area, rooms, level, geom, city, address)
		VALUES ('cian_self', 'cian', 12000000, 54.0, 2, 4,
		        ST_SetSRID(ST_MakePoint(37.6595, 55.7108), 4326), 'msk', 'Мельникова 3к1');`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	repo := NewListingRepo(pool)
	rooms, level := 2, 4
	area := float32(54.0)
	found, err := repo.FindSimilar(ctx, 37.6595, 55.7108, &rooms, &level, &area, "cian_self")
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("объявление не должно находить само себя: %+v", found)
	}
}

func TestFindSimilarWithoutRoomsReturnsNothing(t *testing.T) {
	pool := testPool(t)
	repo := NewListingRepo(pool)
	area := float32(54.0)
	found, err := repo.FindSimilar(context.Background(), 37.6595, 55.7108, nil, nil, &area, "x")
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("без комнат и этажа сравнивать не с чем: %+v", found)
	}
}
```

Если `NewListingRepo` принимает не `*pgxpool.Pool`, а другой тип, привести
вызовы в тесте к фактической сигнатуре (`backend/internal/repository/listing_repo.go`).

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/repository/ -run 'TestSnapshot|TestFindSimilar' -v`
Ожидается: FAIL — `undefined: (*ListingRepo).SnapshotByExternalID`.

- [ ] **Step 3: Добавить доменные структуры**

В `backend/internal/domain/owner_listing.go` дописать:

```go
// ListingSnapshot — плоский снимок строки витрины. Нужен предпросмотру импорта:
// когда объявление уже собрано краулером, данные берутся отсюда, и похода в
// Циан не происходит вовсе.
type ListingSnapshot struct {
	ExternalID        string
	Source            string
	City              string
	Price             *int64
	Area              *float32
	KitchenArea       *float32
	Rooms             *int
	Level             *int
	Levels            *int
	Address           string
	Description       string
	Lng               float64
	Lat               float64
	Photos            []string
	WindowOrientation []string
	SourceURL         string
	OwnerManaged      bool
}

// SimilarListing — кандидат в дубли. Показывается продавцу как предупреждение,
// не как отказ: перевыставленная под новым id квартира — обычное дело.
type SimilarListing struct {
	ExternalID string
	Address    string
	Price      *int64
	Area       *float32
}
```

- [ ] **Step 4: Реализовать методы репозитория**

В `backend/internal/repository/listing_repo.go` дописать:

```go
func (r *ListingRepo) SnapshotByExternalID(ctx context.Context, externalID string) (domain.ListingSnapshot, error) {
	var s domain.ListingSnapshot
	err := r.pool.QueryRow(ctx, `
		SELECT external_id, source, coalesce(city, 'msk'), price, area, kitchen_area,
		       rooms, level, levels, coalesce(address, ''), coalesce(description, ''),
		       ST_X(geom), ST_Y(geom), coalesce(photos, '{}'),
		       coalesce(window_orientation, '{}'), coalesce(source_url, ''), owner_managed
		FROM listings WHERE external_id = $1`, externalID).Scan(
		&s.ExternalID, &s.Source, &s.City, &s.Price, &s.Area, &s.KitchenArea,
		&s.Rooms, &s.Level, &s.Levels, &s.Address, &s.Description,
		&s.Lng, &s.Lat, &s.Photos, &s.WindowOrientation, &s.SourceURL, &s.OwnerManaged)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ListingSnapshot{}, ErrNotFound
	}
	return s, err
}

// FindSimilar ищет ту же квартиру, перевыставленную под другим id: тот же дом
// (150 м), те же комнаты и этаж, площадь в пределах метра. Без комнат и этажа
// сравнивать не с чем — возвращаем пусто, чтобы не сыпать ложными дублями.
func (r *ListingRepo) FindSimilar(ctx context.Context, lng, lat float64,
	rooms, level *int, area *float32, excludeExternalID string) ([]domain.SimilarListing, error) {
	if rooms == nil || level == nil || area == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT external_id, coalesce(address, ''), price, area
		FROM listings
		WHERE is_active
		  AND rooms = $3
		  AND level = $4
		  AND abs(area - $5) <= 1.0
		  AND external_id <> $6
		  AND ST_DWithin(geom::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 150)
		ORDER BY geom <-> ST_SetSRID(ST_MakePoint($1, $2), 4326)
		LIMIT 3`, lng, lat, *rooms, *level, *area, excludeExternalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.SimilarListing{}
	for rows.Next() {
		var s domain.SimilarListing
		if err := rows.Scan(&s.ExternalID, &s.Address, &s.Price, &s.Area); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
```

Проверить, что `errors`, `pgx` и `domain` уже импортированы в файле.

- [ ] **Step 5: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/repository/ -v`
Ожидается: 0 failed.

- [ ] **Step 6: Коммит**

```bash
git add backend/internal/repository/ backend/internal/domain/owner_listing.go
git commit -m "feat: чтение витрины для привязки объявления и поиска дублей"
```

---

### Task 11: Предпросмотр импорта и привязка объявления

**Files:**
- Create: `backend/internal/service/owner_import_service.go`
- Test: `backend/internal/service/owner_import_service_test.go`

**Interfaces:**
- Consumes: `cian.ParseOfferURL`, `cian.ErrNotAnOfferURL`, `cian.ErrOfferNotFound`,
  `cian.ErrBlocked`, `cian.Listing`, `cian.RateLimiter`, `cian.UserQuota` (Task 6-8);
  `repository.OwnerListingRepo`, `repository.ErrExternalIDTaken` (Task 3);
  `(*ListingRepo).SnapshotByExternalID`, `FindSimilar` (Task 10); `apperr` (Task 9)
- Produces:
  - `service.OfferFetcher` — интерфейс `FetchByID(ctx context.Context, offerID int64) (cian.Listing, error)`
  - `service.OwnerListingsRepo`, `service.ShowcaseRepo` — интерфейсы над репозиториями (для моков)
  - `service.ImportPreview` — `Verdict string`, `Draft domain.OwnerListing`,
    `Similar []domain.SimilarListing`, `ExistingID *uuid.UUID`
  - `service.NewOwnerImportService(owners OwnerListingsRepo, showcase ShowcaseRepo, fetcher OfferFetcher, limiter *cian.RateLimiter, quota *cian.UserQuota) *OwnerImportService`
  - `(*OwnerImportService).Preview(ctx, userID uuid.UUID, rawURL string) (ImportPreview, error)`
  - `(*OwnerImportService).Import(ctx, userID uuid.UUID, rawURL string) (domain.OwnerListing, error)`
  - Константы вердиктов: `VerdictNew = "new"`, `VerdictClaimable = "claimable"`,
    `VerdictAlreadyYours = "already_yours"`
  Использует Task 13.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/service/owner_import_service_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/cian"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// --- заглушки зависимостей -------------------------------------------------

type fakeOwners struct {
	byExternal map[string]domain.OwnerListing
	created    []domain.OwnerListing
	createErr  error
}

func (f *fakeOwners) GetByExternalID(_ context.Context, externalID string) (domain.OwnerListing, error) {
	if l, ok := f.byExternal[externalID]; ok {
		return l, nil
	}
	return domain.OwnerListing{}, repository.ErrNotFound
}

func (f *fakeOwners) Create(_ context.Context, l domain.OwnerListing) (domain.OwnerListing, error) {
	if f.createErr != nil {
		return domain.OwnerListing{}, f.createErr
	}
	l.ID = uuid.New()
	l.Status = "draft"
	l.Verification = "unverified"
	f.created = append(f.created, l)
	return l, nil
}

type fakeShowcase struct {
	snapshots map[string]domain.ListingSnapshot
	similar   []domain.SimilarListing
}

func (f *fakeShowcase) SnapshotByExternalID(_ context.Context, externalID string) (domain.ListingSnapshot, error) {
	if s, ok := f.snapshots[externalID]; ok {
		return s, nil
	}
	return domain.ListingSnapshot{}, repository.ErrNotFound
}

func (f *fakeShowcase) FindSimilar(_ context.Context, _, _ float64, _, _ *int, _ *float32, _ string) ([]domain.SimilarListing, error) {
	return f.similar, nil
}

type fakeFetcher struct {
	listing cian.Listing
	err     error
	calls   int
}

func (f *fakeFetcher) FetchByID(_ context.Context, _ int64) (cian.Listing, error) {
	f.calls++
	return f.listing, f.err
}

func intp(v int) *int          { return &v }
func f32p(v float32) *float32  { return &v }
func i64p(v int64) *int64      { return &v }
func f64p(v float64) *float64  { return &v }

func newService(owners *fakeOwners, showcase *fakeShowcase, fetcher *fakeFetcher) *OwnerImportService {
	now := func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
	return NewOwnerImportService(owners, showcase, fetcher,
		cian.NewRateLimiter(100, now), cian.NewUserQuota(100, now))
}

func sampleCianListing() cian.Listing {
	return cian.Listing{
		CianID: "318394906", Description: "Тихая двушка",
		Price: i64p(12_500_000), Area: f64p(54.3), Rooms: intp(2),
		Floor: intp(4), Floors: intp(17),
		Address:   "Москва, улица Мельникова, 3к1",
		Photos:    []string{"https://images.cdn-cian.ru/1.jpg"},
		Latitude:  f64p(55.7108), Longitude: f64p(37.6595),
		URL: "https://www.cian.ru/sale/flat/318394906/",
	}
}

// --- тесты -----------------------------------------------------------------

func TestPreviewRejectsGarbageURL(t *testing.T) {
	svc := newService(&fakeOwners{}, &fakeShowcase{}, &fakeFetcher{})
	_, err := svc.Preview(context.Background(), uuid.New(), "моя квартира")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "cian_url_invalid" {
		t.Fatalf("ожидался cian_url_invalid, получено %v", err)
	}
}

func TestPreviewAlreadyYours(t *testing.T) {
	userID := uuid.New()
	existing := domain.OwnerListing{ID: uuid.New(), UserID: userID, ExternalID: "cian_318394906"}
	fetcher := &fakeFetcher{}
	svc := newService(&fakeOwners{byExternal: map[string]domain.OwnerListing{
		"cian_318394906": existing,
	}}, &fakeShowcase{}, fetcher)

	preview, err := svc.Preview(context.Background(), userID, "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictAlreadyYours {
		t.Fatalf("вердикт = %q", preview.Verdict)
	}
	if preview.ExistingID == nil || *preview.ExistingID != existing.ID {
		t.Fatal("должен вернуться id уже существующей карточки")
	}
	if fetcher.calls != 0 {
		t.Fatal("своё объявление не требует похода в Циан")
	}
}

func TestPreviewClaimedByOther(t *testing.T) {
	fetcher := &fakeFetcher{}
	svc := newService(&fakeOwners{byExternal: map[string]domain.OwnerListing{
		"cian_318394906": {ID: uuid.New(), UserID: uuid.New()},
	}}, &fakeShowcase{}, fetcher)

	_, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "listing_claimed_by_other" {
		t.Fatalf("ожидался listing_claimed_by_other, получено %v", err)
	}
	if fetcher.calls != 0 {
		t.Fatal("чужое объявление не требует похода в Циан")
	}
}

func TestPreviewClaimableSkipsCian(t *testing.T) {
	fetcher := &fakeFetcher{}
	showcase := &fakeShowcase{snapshots: map[string]domain.ListingSnapshot{
		"cian_318394906": {
			ExternalID: "cian_318394906", Source: "cian", City: "msk",
			Price: i64p(12_500_000), Area: f32p(54.3), Rooms: intp(2),
			Level: intp(4), Levels: intp(17),
			Address: "Москва, улица Мельникова, 3к1", Lng: 37.6595, Lat: 55.7108,
			Photos: []string{"https://images.cdn-cian.ru/1.jpg"},
		},
	}}
	svc := newService(&fakeOwners{}, showcase, fetcher)

	preview, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictClaimable {
		t.Fatalf("вердикт = %q", preview.Verdict)
	}
	if fetcher.calls != 0 {
		t.Fatal("объявление уже в базе — идти в Циан незачем")
	}
	if preview.Draft.Address != "Москва, улица Мельникова, 3к1" || *preview.Draft.Rooms != 2 {
		t.Fatalf("черновик собран неверно: %+v", preview.Draft)
	}
}

func TestPreviewNewGoesToCian(t *testing.T) {
	fetcher := &fakeFetcher{listing: sampleCianListing()}
	svc := newService(&fakeOwners{}, &fakeShowcase{}, fetcher)

	preview, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictNew {
		t.Fatalf("вердикт = %q", preview.Verdict)
	}
	if fetcher.calls != 1 {
		t.Fatalf("ожидался один запрос в Циан, было %d", fetcher.calls)
	}
	if preview.Draft.ExternalID != "cian_318394906" || preview.Draft.Origin != "cian" {
		t.Fatalf("черновик собран неверно: %+v", preview.Draft)
	}
	if *preview.Draft.Level != 4 || *preview.Draft.Levels != 17 {
		t.Fatalf("этаж/этажность не перенесены: %+v", preview.Draft)
	}
}

func TestPreviewSurfacesSimilarAlongsideVerdict(t *testing.T) {
	fetcher := &fakeFetcher{listing: sampleCianListing()}
	showcase := &fakeShowcase{similar: []domain.SimilarListing{
		{ExternalID: "cian_777", Address: "Мельникова 3к1", Price: i64p(12_000_000)},
	}}
	svc := newService(&fakeOwners{}, showcase, fetcher)

	preview, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictNew {
		t.Fatalf("похожий объект не должен менять вердикт, получено %q", preview.Verdict)
	}
	if len(preview.Similar) != 1 {
		t.Fatalf("похожие не доехали: %+v", preview.Similar)
	}
}

func TestPreviewMapsCianBlockToUnavailable(t *testing.T) {
	svc := newService(&fakeOwners{}, &fakeShowcase{}, &fakeFetcher{err: cian.ErrBlocked})

	_, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "cian_unavailable" {
		t.Fatalf("ожидался cian_unavailable, получено %v", err)
	}
}

func TestPreviewMapsOfferNotFound(t *testing.T) {
	svc := newService(&fakeOwners{}, &fakeShowcase{}, &fakeFetcher{err: cian.ErrOfferNotFound})

	_, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "cian_offer_not_found" {
		t.Fatalf("ожидался cian_offer_not_found, получено %v", err)
	}
}

func TestPreviewRespectsUserQuota(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
	svc := NewOwnerImportService(&fakeOwners{}, &fakeShowcase{},
		&fakeFetcher{listing: sampleCianListing()},
		cian.NewRateLimiter(100, now), cian.NewUserQuota(1, now))
	userID := uuid.New()
	url := "https://www.cian.ru/sale/flat/318394906/"

	if _, err := svc.Preview(context.Background(), userID, url); err != nil {
		t.Fatalf("первый импорт: %v", err)
	}
	_, err := svc.Preview(context.Background(), userID, url)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "rate_limited" {
		t.Fatalf("ожидался rate_limited, получено %v", err)
	}
}

func TestImportCreatesOwnerListing(t *testing.T) {
	owners := &fakeOwners{}
	svc := newService(owners, &fakeShowcase{}, &fakeFetcher{listing: sampleCianListing()})
	userID := uuid.New()

	created, err := svc.Import(context.Background(), userID, "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if created.ExternalID != "cian_318394906" || created.UserID != userID {
		t.Fatalf("создано не то: %+v", created)
	}
	if len(owners.created) != 1 {
		t.Fatalf("ожидалась одна вставка, было %d", len(owners.created))
	}
}

func TestImportRaceLosesToUniqueConstraint(t *testing.T) {
	owners := &fakeOwners{createErr: repository.ErrExternalIDTaken}
	svc := newService(owners, &fakeShowcase{}, &fakeFetcher{listing: sampleCianListing()})

	_, err := svc.Import(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "listing_claimed_by_other" {
		t.Fatalf("ожидался listing_claimed_by_other, получено %v", err)
	}
}
```

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/service/ -run 'TestPreview|TestImport' -v`
Ожидается: FAIL — `undefined: NewOwnerImportService`.

- [ ] **Step 3: Реализовать сервис**

Создать `backend/internal/service/owner_import_service.go`:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/cian"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// Вердикты предпросмотра — три взаимоисключающих состояния самого объявления.
// Похожие объекты ортогональны вердикту и едут отдельным полем: новое
// объявление вполне может иметь похожего соседа.
const (
	VerdictNew          = "new"
	VerdictClaimable    = "claimable"
	VerdictAlreadyYours = "already_yours"
)

type OfferFetcher interface {
	FetchByID(ctx context.Context, offerID int64) (cian.Listing, error)
}

type OwnerListingsRepo interface {
	GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error)
	Create(ctx context.Context, l domain.OwnerListing) (domain.OwnerListing, error)
}

type ShowcaseRepo interface {
	SnapshotByExternalID(ctx context.Context, externalID string) (domain.ListingSnapshot, error)
	FindSimilar(ctx context.Context, lng, lat float64, rooms, level *int, area *float32, excludeExternalID string) ([]domain.SimilarListing, error)
}

type ImportPreview struct {
	Verdict    string
	Draft      domain.OwnerListing
	Similar    []domain.SimilarListing
	ExistingID *uuid.UUID
}

type OwnerImportService struct {
	owners   OwnerListingsRepo
	showcase ShowcaseRepo
	fetcher  OfferFetcher
	limiter  *cian.RateLimiter
	quota    *cian.UserQuota
}

func NewOwnerImportService(owners OwnerListingsRepo, showcase ShowcaseRepo,
	fetcher OfferFetcher, limiter *cian.RateLimiter, quota *cian.UserQuota) *OwnerImportService {
	return &OwnerImportService{owners: owners, showcase: showcase,
		fetcher: fetcher, limiter: limiter, quota: quota}
}

// Preview разбирает ссылку и возвращает то, что продавец увидит до импорта.
// Проверки идут от дешёвых к дорогим: свой кабинет → чужой кабинет → витрина →
// и только потом Циан. На собранной базе большинство ссылок закрывается
// третьим шагом, без единого исходящего запроса.
func (s *OwnerImportService) Preview(ctx context.Context, userID uuid.UUID, rawURL string) (ImportPreview, error) {
	offerID, err := cian.ParseOfferURL(rawURL)
	if err != nil {
		return ImportPreview{}, apperr.CianURLInvalid()
	}
	externalID := fmt.Sprintf("cian_%d", offerID)

	existing, err := s.owners.GetByExternalID(ctx, externalID)
	switch {
	case err == nil && existing.UserID == userID:
		id := existing.ID
		return ImportPreview{Verdict: VerdictAlreadyYours, Draft: existing, ExistingID: &id}, nil
	case err == nil:
		return ImportPreview{}, apperr.ListingClaimedByOther()
	case !errors.Is(err, repository.ErrNotFound):
		return ImportPreview{}, err
	}

	draft, err := s.draftFromShowcase(ctx, externalID)
	verdict := VerdictClaimable
	if errors.Is(err, repository.ErrNotFound) {
		draft, err = s.draftFromCian(ctx, userID, offerID, externalID)
		verdict = VerdictNew
	}
	if err != nil {
		return ImportPreview{}, err
	}
	draft.UserID = userID

	similar, err := s.showcase.FindSimilar(ctx, draft.Lng, draft.Lat,
		draft.Rooms, draft.Level, draft.Area, externalID)
	if err != nil {
		return ImportPreview{}, err
	}
	return ImportPreview{Verdict: verdict, Draft: draft, Similar: similar}, nil
}

// Import выполняет то же ветвление и создаёт карточку в кабинете.
func (s *OwnerImportService) Import(ctx context.Context, userID uuid.UUID, rawURL string) (domain.OwnerListing, error) {
	preview, err := s.Preview(ctx, userID, rawURL)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	if preview.Verdict == VerdictAlreadyYours {
		return preview.Draft, nil
	}

	created, err := s.owners.Create(ctx, preview.Draft)
	if errors.Is(err, repository.ErrExternalIDTaken) {
		// Гонка двух одновременных импортов одной ссылки: проверку выше прошли
		// оба, вставку выиграл один. UNIQUE — единственная надёжная арбитражная
		// точка, поэтому конфликт разбирается здесь, а не предварительной блокировкой.
		return domain.OwnerListing{}, apperr.ListingClaimedByOther()
	}
	return created, err
}

func (s *OwnerImportService) draftFromShowcase(ctx context.Context, externalID string) (domain.OwnerListing, error) {
	snapshot, err := s.showcase.SnapshotByExternalID(ctx, externalID)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	return domain.OwnerListing{
		ExternalID: snapshot.ExternalID, Origin: "cian", City: snapshot.City,
		Price: snapshot.Price, Area: snapshot.Area, KitchenArea: snapshot.KitchenArea,
		Rooms: snapshot.Rooms, Level: snapshot.Level, Levels: snapshot.Levels,
		Address: snapshot.Address, Lng: snapshot.Lng, Lat: snapshot.Lat,
		WindowOrientation: snapshot.WindowOrientation, Description: snapshot.Description,
		Photos: snapshot.Photos, SourceURL: snapshot.SourceURL,
	}, nil
}

func (s *OwnerImportService) draftFromCian(ctx context.Context, userID uuid.UUID,
	offerID int64, externalID string) (domain.OwnerListing, error) {
	if !s.quota.Allow(userID.String()) {
		return domain.OwnerListing{}, apperr.RateLimited(
			"Слишком много импортов за час. Попробуйте позже")
	}
	if !s.limiter.Allow() {
		return domain.OwnerListing{}, apperr.CianUnavailable()
	}

	listing, err := s.fetcher.FetchByID(ctx, offerID)
	switch {
	case errors.Is(err, cian.ErrOfferNotFound):
		return domain.OwnerListing{}, apperr.CianOfferNotFound()
	case errors.Is(err, cian.ErrBlocked):
		return domain.OwnerListing{}, apperr.CianUnavailable()
	case err != nil:
		return domain.OwnerListing{}, apperr.CianUnavailable()
	}
	return listingToDraft(listing, externalID), nil
}

// listingToDraft переводит модель парсера в карточку кабинета. Циан отдаёт
// площадь float64, витрина хранит real — сужение здесь, а не в репозитории,
// чтобы преобразование было видно в одном месте.
func listingToDraft(l cian.Listing, externalID string) domain.OwnerListing {
	draft := domain.OwnerListing{
		ExternalID: externalID, Origin: "cian", City: "msk",
		Price: l.Price, Rooms: l.Rooms, Level: l.Floor, Levels: l.Floors,
		Address: strings.TrimSpace(l.Address), Description: strings.TrimSpace(l.Description),
		Photos: l.Photos, SourceURL: l.URL,
	}
	if l.Area != nil {
		area := float32(*l.Area)
		draft.Area = &area
	}
	if l.Longitude != nil {
		draft.Lng = *l.Longitude
	}
	if l.Latitude != nil {
		draft.Lat = *l.Latitude
	}
	if draft.Photos == nil {
		draft.Photos = []string{}
	}
	if draft.WindowOrientation == nil {
		draft.WindowOrientation = []string{}
	}
	return draft
}
```

Город у объявления с Циана берётся `"msk"`: батч-парсер настроен на регион 1,
и других регионов в витрине сейчас нет. Когда появится второй регион, город
будет выводиться из `CianRegion` — но выдумывать это отображение сейчас нельзя.

- [ ] **Step 4: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/service/ -run 'TestPreview|TestImport' -v`
Ожидается: 11 passed.

- [ ] **Step 5: Прогнать весь Go-набор**

Запустить: `cd backend && go vet ./... && go test ./...`
Ожидается: 0 failed.

- [ ] **Step 6: Коммит**

```bash
git add backend/internal/service/owner_import_service.go backend/internal/service/owner_import_service_test.go
git commit -m "feat: предпросмотр импорта с Циана и привязка объявления к кабинету"
```

---

### Task 12: Управление объявлением и публикация в витрину

**Files:**
- Create: `backend/internal/service/owner_listing_service.go`
- Test: `backend/internal/service/owner_listing_service_test.go`

**Interfaces:**
- Consumes: `repository.OwnerListingRepo`, `repository.ErrNotFound`,
  `repository.ErrExternalIDTaken` (Task 3); `client.MLClient`,
  `client.OwnerUpsertRequest`, `client.OwnerListingInvalidError` (Task 9); `apperr`
- Produces:
  - `service.OwnerStore` — интерфейс над `OwnerListingRepo` (полный набор методов Task 3)
  - `service.Publisher` — интерфейс `OwnerUpsert`, `OwnerWithdraw` над ML-клиентом
  - `service.NewOwnerListingService(store OwnerStore, publisher Publisher, autopublish bool) *OwnerListingService`
  - Методы: `List(ctx, userID)`, `Get(ctx, userID, id)`,
    `CreateManual(ctx, userID, draft domain.OwnerListing)`,
    `Update(ctx, userID, id, f domain.OwnerListingFields)`,
    `SetPhotos(ctx, userID, id uuid.UUID, photos []string)`,
    `Publish(ctx, userID, id)`, `Unpublish(ctx, userID, id)`,
    `Delete(ctx, userID, id)`, `Autopublish() bool`
  Использует Task 13 и Task 14.

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/service/owner_listing_service_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type fakeStore struct {
	items      map[uuid.UUID]domain.OwnerListing
	statusLog  []string
	createErr  error
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: map[uuid.UUID]domain.OwnerListing{}}
}

func (f *fakeStore) put(l domain.OwnerListing) domain.OwnerListing {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	f.items[l.ID] = l
	return l
}

func (f *fakeStore) Create(_ context.Context, l domain.OwnerListing) (domain.OwnerListing, error) {
	if f.createErr != nil {
		return domain.OwnerListing{}, f.createErr
	}
	l.Status, l.Verification = "draft", "unverified"
	return f.put(l), nil
}

func (f *fakeStore) GetOwned(_ context.Context, id, userID uuid.UUID) (domain.OwnerListing, error) {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return domain.OwnerListing{}, repository.ErrNotFound
	}
	return l, nil
}

func (f *fakeStore) GetByExternalID(_ context.Context, externalID string) (domain.OwnerListing, error) {
	for _, l := range f.items {
		if l.ExternalID == externalID {
			return l, nil
		}
	}
	return domain.OwnerListing{}, repository.ErrNotFound
}

func (f *fakeStore) List(_ context.Context, userID uuid.UUID) ([]domain.OwnerListing, error) {
	out := []domain.OwnerListing{}
	for _, l := range f.items {
		if l.UserID == userID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateFields(_ context.Context, id, userID uuid.UUID, fields domain.OwnerListingFields) (domain.OwnerListing, error) {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return domain.OwnerListing{}, repository.ErrNotFound
	}
	if fields.Price != nil {
		l.Price = fields.Price
	}
	if fields.Description != nil {
		l.Description = *fields.Description
	}
	if fields.City != nil {
		l.City = *fields.City
	}
	f.items[id] = l
	return l, nil
}

func (f *fakeStore) SetPhotos(_ context.Context, id, userID uuid.UUID, photos []string) (domain.OwnerListing, error) {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return domain.OwnerListing{}, repository.ErrNotFound
	}
	l.Photos = photos
	f.items[id] = l
	return l, nil
}

func (f *fakeStore) SetStatus(_ context.Context, id uuid.UUID, status, importError string) error {
	l, ok := f.items[id]
	if !ok {
		return repository.ErrNotFound
	}
	l.Status, l.ImportError = status, importError
	f.items[id] = l
	f.statusLog = append(f.statusLog, status)
	return nil
}

func (f *fakeStore) Delete(_ context.Context, id, userID uuid.UUID) error {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return repository.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

type fakePublisher struct {
	upsertErr   error
	indexed     bool
	withdrawn   []string
	lastRequest client.OwnerUpsertRequest
}

func (p *fakePublisher) OwnerUpsert(_ context.Context, req client.OwnerUpsertRequest) (*client.OwnerUpsertResponse, error) {
	p.lastRequest = req
	if p.upsertErr != nil {
		return nil, p.upsertErr
	}
	return &client.OwnerUpsertResponse{ExternalID: req.ExternalID, Indexed: p.indexed}, nil
}

func (p *fakePublisher) OwnerWithdraw(_ context.Context, externalID string) (*client.OwnerWithdrawResponse, error) {
	p.withdrawn = append(p.withdrawn, externalID)
	return &client.OwnerWithdrawResponse{ExternalID: externalID, Deactivated: true}, nil
}

func publishableDraft(userID uuid.UUID) domain.OwnerListing {
	price := int64(12_000_000)
	area := float32(54.0)
	rooms, level, levels := 2, 4, 17
	return domain.OwnerListing{
		UserID: userID, ExternalID: "owner_abc", Origin: "manual", Status: "draft",
		City: "msk", Price: &price, Area: &area, Rooms: &rooms, Level: &level, Levels: &levels,
		Address: "Москва, Тверская 1", Lng: 37.6055, Lat: 55.7601,
		Description: "Светлая двушка", Photos: []string{}, WindowOrientation: []string{},
	}
}

func TestCreateManualGeneratesOwnerExternalID(t *testing.T) {
	store := newFakeStore()
	svc := NewOwnerListingService(store, &fakePublisher{}, true)
	userID := uuid.New()

	created, err := svc.CreateManual(context.Background(), userID, publishableDraft(userID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(created.ExternalID, "owner_") {
		t.Fatalf("external_id = %q, ожидался префикс owner_", created.ExternalID)
	}
	if created.Origin != "manual" || created.Status != "draft" {
		t.Fatalf("создано не как ручной черновик: %+v", created)
	}
}

func TestPublishSendsShowcasePayloadAndMarksPublished(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	published, err := svc.Publish(context.Background(), userID, draft.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if published.Status != "published" {
		t.Fatalf("статус = %q", published.Status)
	}
	if publisher.lastRequest.Source != "owner" || publisher.lastRequest.City != "msk" {
		t.Fatalf("на витрину ушло не то: %+v", publisher.lastRequest)
	}
	if publisher.lastRequest.Lng != 37.6055 {
		t.Fatalf("координаты не доехали: %+v", publisher.lastRequest)
	}
	// Промежуточный publishing обязателен: продавец видит, что идёт работа,
	// а не «ничего не произошло» на время расчёта эмбеддинга.
	if len(store.statusLog) < 2 || store.statusLog[0] != "publishing" {
		t.Fatalf("статусная машина: %+v", store.statusLog)
	}
}

func TestPublishOfImportedListingKeepsCianSource(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := publishableDraft(userID)
	draft.ExternalID, draft.Origin = "cian_318394906", "cian"
	stored := store.put(draft)

	if _, err := svc.Publish(context.Background(), userID, stored.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publisher.lastRequest.Source != "cian" {
		t.Fatalf("source = %q: у импортированного объявления источник остаётся cian",
			publisher.lastRequest.Source)
	}
}

func TestPublishFailureLeavesRecoverableState(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{upsertErr: errors.New("ml down")}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	_, err := svc.Publish(context.Background(), userID, draft.ID)
	if err == nil {
		t.Fatal("ошибка ML должна доезжать до продавца")
	}
	if store.items[draft.ID].Status != "failed" {
		t.Fatalf("статус = %q, ожидался failed", store.items[draft.ID].Status)
	}
	if store.items[draft.ID].ImportError == "" {
		t.Fatal("причина провала должна сохраняться — по ней рисуется кнопка «Повторить»")
	}
}

func TestPublishSurfacesValidationField(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{upsertErr: &client.OwnerListingInvalidError{
		Field: "coordinates", Message: "Координаты вне границ выбранного города"}}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	_, err := svc.Publish(context.Background(), userID, draft.ID)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "owner_listing_invalid" {
		t.Fatalf("ожидался owner_listing_invalid, получено %v", err)
	}
	if !strings.Contains(appErr.Message, "coordinates") {
		t.Fatalf("сообщение должно называть поле: %q", appErr.Message)
	}
}

func TestPublishRejectsNotIndexed(t *testing.T) {
	store := newFakeStore()
	svc := NewOwnerListingService(store, &fakePublisher{indexed: false}, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	_, err := svc.Publish(context.Background(), userID, draft.ID)
	if err == nil {
		t.Fatal("объект без эмбеддинга не находится поиском — это провал публикации")
	}
	if store.items[draft.ID].Status != "failed" {
		t.Fatalf("статус = %q", store.items[draft.ID].Status)
	}
}

func TestUnpublishWithdrawsFromShowcase(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := publishableDraft(userID)
	draft.Status = "published"
	stored := store.put(draft)

	updated, err := svc.Unpublish(context.Background(), userID, stored.ID)
	if err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if updated.Status != "unpublished" {
		t.Fatalf("статус = %q", updated.Status)
	}
	if len(publisher.withdrawn) != 1 || publisher.withdrawn[0] != "owner_abc" {
		t.Fatalf("витрина не была уведомлена: %+v", publisher.withdrawn)
	}
}

func TestDeleteWithdrawsBeforeRemoving(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := publishableDraft(userID)
	draft.Status = "published"
	stored := store.put(draft)

	if err := svc.Delete(context.Background(), userID, stored.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(publisher.withdrawn) != 1 {
		t.Fatal("удаление опубликованного объявления обязано снять его с витрины")
	}
	if _, ok := store.items[stored.ID]; ok {
		t.Fatal("карточка должна быть удалена")
	}
}

func TestGetHidesForeignListing(t *testing.T) {
	store := newFakeStore()
	svc := NewOwnerListingService(store, &fakePublisher{}, true)
	stored := store.put(publishableDraft(uuid.New()))

	_, err := svc.Get(context.Background(), uuid.New(), stored.ID)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "owner_listing_not_found" {
		t.Fatalf("чужое объявление должно быть неотличимо от несуществующего, получено %v", err)
	}
}
```

Добавить `"strings"` в импорты тест-файла.

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/service/ -run 'TestCreateManual|TestPublish|TestUnpublish|TestDelete|TestGetHides' -v`
Ожидается: FAIL — `undefined: NewOwnerListingService`.

- [ ] **Step 3: Реализовать сервис**

Создать `backend/internal/service/owner_listing_service.go`:

```go
package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type OwnerStore interface {
	Create(ctx context.Context, l domain.OwnerListing) (domain.OwnerListing, error)
	GetOwned(ctx context.Context, id, userID uuid.UUID) (domain.OwnerListing, error)
	GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error)
	List(ctx context.Context, userID uuid.UUID) ([]domain.OwnerListing, error)
	UpdateFields(ctx context.Context, id, userID uuid.UUID, f domain.OwnerListingFields) (domain.OwnerListing, error)
	SetPhotos(ctx context.Context, id, userID uuid.UUID, photos []string) (domain.OwnerListing, error)
	SetStatus(ctx context.Context, id uuid.UUID, status, importError string) error
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

type Publisher interface {
	OwnerUpsert(ctx context.Context, req client.OwnerUpsertRequest) (*client.OwnerUpsertResponse, error)
	OwnerWithdraw(ctx context.Context, externalID string) (*client.OwnerWithdrawResponse, error)
}

type OwnerListingService struct {
	store       OwnerStore
	publisher   Publisher
	autopublish bool
}

func NewOwnerListingService(store OwnerStore, publisher Publisher, autopublish bool) *OwnerListingService {
	return &OwnerListingService{store: store, publisher: publisher, autopublish: autopublish}
}

// Autopublish сообщает вызывающему, публиковать ли объявление сразу после
// импорта или создания. Рубильник живёт в сервисе, а не в хендлере: решение
// продуктовое, и хендлер о нём знать не должен.
func (s *OwnerListingService) Autopublish() bool { return s.autopublish }

func (s *OwnerListingService) List(ctx context.Context, userID uuid.UUID) ([]domain.OwnerListing, error) {
	return s.store.List(ctx, userID)
}

func (s *OwnerListingService) Get(ctx context.Context, userID, id uuid.UUID) (domain.OwnerListing, error) {
	l, err := s.store.GetOwned(ctx, id, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.OwnerListing{}, apperr.OwnerListingNotFound()
	}
	return l, err
}

// CreateManual заводит объявление, которого нет ни на Циане, ни в витрине.
// external_id синтезируется: у ручного объявления нет внешнего идентификатора,
// а витрине он нужен как единственный ключ объекта.
func (s *OwnerListingService) CreateManual(ctx context.Context, userID uuid.UUID, draft domain.OwnerListing) (domain.OwnerListing, error) {
	draft.UserID = userID
	draft.Origin = "manual"
	draft.ExternalID = "owner_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	draft.SourceURL = ""
	if draft.Photos == nil {
		draft.Photos = []string{}
	}
	if draft.WindowOrientation == nil {
		draft.WindowOrientation = []string{}
	}
	if err := validateCity(draft.City); err != nil {
		return domain.OwnerListing{}, err
	}

	created, err := s.store.Create(ctx, draft)
	if errors.Is(err, repository.ErrExternalIDTaken) {
		return domain.OwnerListing{}, apperr.Internal("не удалось выделить идентификатор объявления")
	}
	return created, err
}

func (s *OwnerListingService) Update(ctx context.Context, userID, id uuid.UUID, f domain.OwnerListingFields) (domain.OwnerListing, error) {
	if f.City != nil {
		if err := validateCity(*f.City); err != nil {
			return domain.OwnerListing{}, err
		}
	}
	updated, err := s.store.UpdateFields(ctx, id, userID, f)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.OwnerListing{}, apperr.OwnerListingNotFound()
	}
	return updated, err
}

func (s *OwnerListingService) SetPhotos(ctx context.Context, userID, id uuid.UUID, photos []string) (domain.OwnerListing, error) {
	updated, err := s.store.SetPhotos(ctx, id, userID, photos)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.OwnerListing{}, apperr.OwnerListingNotFound()
	}
	return updated, err
}

// Publish отдаёт объявление витрине и переводит статус.
//
// Промежуточный publishing выставляется до вызова ML: расчёт эмбеддинга
// занимает секунды, и без него продавец видит «ничего не произошло».
// Провал оставляет failed с причиной — расхождение кабинета и витрины видно
// продавцу и лечится кнопкой «Повторить», а не копится молча.
func (s *OwnerListingService) Publish(ctx context.Context, userID, id uuid.UUID) (domain.OwnerListing, error) {
	listing, err := s.Get(ctx, userID, id)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	if err := s.store.SetStatus(ctx, id, "publishing", ""); err != nil {
		return domain.OwnerListing{}, err
	}

	source := "owner"
	if listing.Origin == "cian" {
		// У импортированного объявления источник остаётся cian: id стабилен,
		// и при следующем обходе краулер узнаёт объект как свой.
		source = "cian"
	}
	resp, err := s.publisher.OwnerUpsert(ctx, client.OwnerUpsertRequest{
		ExternalID: listing.ExternalID, Source: source, City: listing.City,
		Price: listing.Price, Area: listing.Area, KitchenArea: listing.KitchenArea,
		Rooms: listing.Rooms, Level: listing.Level, Levels: listing.Levels,
		Address: listing.Address, Lng: listing.Lng, Lat: listing.Lat,
		WindowOrientation: listing.WindowOrientation, Description: listing.Description,
		Photos: listing.Photos, SourceURL: listing.SourceURL,
	})

	var invalid *client.OwnerListingInvalidError
	switch {
	case errors.As(err, &invalid):
		_ = s.store.SetStatus(ctx, id, "failed", invalid.Message)
		return domain.OwnerListing{}, apperr.OwnerListingInvalid(invalid.Field, invalid.Message)
	case err != nil:
		_ = s.store.SetStatus(ctx, id, "failed", err.Error())
		return domain.OwnerListing{}, apperr.Internal("Витрина не приняла объявление. Попробуйте ещё раз")
	case !resp.Indexed:
		// Объект без вектора лежит в базе и не находится семантическим
		// поиском — это хуже отсутствия, поэтому публикация считается провальной.
		_ = s.store.SetStatus(ctx, id, "failed", "объявление не проиндексировано")
		return domain.OwnerListing{}, apperr.Internal("Объявление не удалось проиндексировать. Попробуйте ещё раз")
	}

	if err := s.store.SetStatus(ctx, id, "published", ""); err != nil {
		return domain.OwnerListing{}, err
	}
	return s.Get(ctx, userID, id)
}

func (s *OwnerListingService) Unpublish(ctx context.Context, userID, id uuid.UUID) (domain.OwnerListing, error) {
	listing, err := s.Get(ctx, userID, id)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	if _, err := s.publisher.OwnerWithdraw(ctx, listing.ExternalID); err != nil {
		return domain.OwnerListing{}, apperr.Internal("Не удалось снять объявление с публикации")
	}
	if err := s.store.SetStatus(ctx, id, "unpublished", ""); err != nil {
		return domain.OwnerListing{}, err
	}
	return s.Get(ctx, userID, id)
}

// Delete сначала гасит объект в витрине и только потом удаляет карточку:
// обратный порядок оставил бы в поиске объявление, которым уже никто не владеет.
func (s *OwnerListingService) Delete(ctx context.Context, userID, id uuid.UUID) error {
	listing, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if listing.Status == "published" || listing.Status == "publishing" {
		if _, err := s.publisher.OwnerWithdraw(ctx, listing.ExternalID); err != nil {
			return apperr.Internal("Не удалось снять объявление с публикации")
		}
	}
	err = s.store.Delete(ctx, id, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperr.OwnerListingNotFound()
	}
	return err
}

func validateCity(city string) error {
	if city != "msk" && city != "spb" {
		return apperr.Validation("Город должен быть msk или spb")
	}
	return nil
}
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Запустить: `cd backend && go test ./internal/service/ -v`
Ожидается: 0 failed.

- [ ] **Step 5: Коммит**

```bash
git add backend/internal/service/owner_listing_service.go backend/internal/service/owner_listing_service_test.go
git commit -m "feat: управление объявлением продавца и публикация в витрину"
```

---

### Task 13: HTTP-ручки кабинета и проводка

**Files:**
- Create: `backend/internal/http/handlers/owner_handler.go`
- Modify: `backend/internal/http/router.go` (группа `/owner`)
- Modify: `backend/cmd/api/main.go` (сборка зависимостей)
- Test: `backend/internal/http/handlers/owner_handler_test.go`

**Interfaces:**
- Consumes: `service.OwnerListingService`, `service.OwnerImportService`,
  `service.ImportPreview`, вердикты (Task 11, 12); `middleware.UserID`; `apperr`
- Produces:
  - `handlers.NewOwnerHandler(listings *service.OwnerListingService, imports *service.OwnerImportService) *OwnerHandler`
  - Методы-хендлеры: `List`, `Get`, `Create`, `Update`, `ImportPreview`, `Import`,
    `Publish`, `Unpublish`, `Delete`
  - `handlers.OwnerListingDTO(l domain.OwnerListing) fiber.Map` — форма ответа,
    её же читает фронт (Task 15)
  Использует Task 14 (добавляет к тому же хендлеру ручки фото).

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/http/handlers/owner_handler_test.go`:

```go
package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

func TestOwnerListingDTOShape(t *testing.T) {
	price := int64(12_500_000)
	area := float32(54.3)
	rooms, level, levels := 2, 4, 17
	published := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	dto := OwnerListingDTO(domain.OwnerListing{
		ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExternalID: "cian_318394906", Origin: "cian", Status: "published",
		Verification: "unverified", City: "msk",
		Price: &price, Area: &area, Rooms: &rooms, Level: &level, Levels: &levels,
		Address: "Москва, улица Мельникова, 3к1", Lng: 37.6595, Lat: 55.7108,
		Description: "Тихая двушка", Photos: []string{"https://images.cdn-cian.ru/1.jpg"},
		WindowOrientation: []string{"юг"},
		SourceURL: "https://www.cian.ru/sale/flat/318394906/",
		PublishedAt: &published,
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	coords, ok := got["coordinates"].([]any)
	if !ok || len(coords) != 2 {
		t.Fatalf("coordinates должны быть парой: %v", got["coordinates"])
	}
	// Контракт проекта: везде [lng, lat], без исключений.
	if coords[0].(float64) != 37.6595 || coords[1].(float64) != 55.7108 {
		t.Fatalf("порядок координат нарушен: %v", coords)
	}
	for _, key := range []string{"id", "external_id", "origin", "status",
		"verification", "city", "price", "area", "rooms", "level", "levels",
		"address", "description", "photos", "window_orientation", "source_url",
		"published_at", "updated_at"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("в ответе нет поля %q", key)
		}
	}
}

func TestOwnerListingDTONullsAreExplicit(t *testing.T) {
	dto := OwnerListingDTO(domain.OwnerListing{
		ID: uuid.New(), Status: "draft", Photos: []string{}, WindowOrientation: []string{},
	})
	raw, _ := json.Marshal(dto)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)

	// Цена незаполненного черновика — null, а не 0: синтетический ноль вместо
	// отсутствующего значения запрещён правилами проекта.
	if got["price"] != nil {
		t.Fatalf("price = %v, ожидался null", got["price"])
	}
	if got["published_at"] != nil {
		t.Fatalf("published_at = %v, ожидался null", got["published_at"])
	}
	if photos, ok := got["photos"].([]any); !ok || photos == nil {
		t.Fatalf("photos должны быть пустым массивом, а не null: %v", got["photos"])
	}
}
```

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/http/handlers/ -run TestOwnerListingDTO -v`
Ожидается: FAIL — `undefined: OwnerListingDTO`.

- [ ] **Step 3: Написать хендлер**

Создать `backend/internal/http/handlers/owner_handler.go`:

```go
package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type OwnerHandler struct {
	listings *service.OwnerListingService
	imports  *service.OwnerImportService
}

func NewOwnerHandler(listings *service.OwnerListingService, imports *service.OwnerImportService) *OwnerHandler {
	return &OwnerHandler{listings: listings, imports: imports}
}

// OwnerListingDTO — форма ответа кабинета. Координаты отдаются парой
// [lng, lat] тем же контрактом, что и везде в проекте: фронт не делает
// никаких преобразований.
func OwnerListingDTO(l domain.OwnerListing) fiber.Map {
	return fiber.Map{
		"id":                 l.ID,
		"external_id":        l.ExternalID,
		"origin":             l.Origin,
		"status":             l.Status,
		"verification":       l.Verification,
		"city":               l.City,
		"price":              l.Price,
		"area":               l.Area,
		"kitchen_area":       l.KitchenArea,
		"rooms":              l.Rooms,
		"level":              l.Level,
		"levels":             l.Levels,
		"address":            l.Address,
		"coordinates":        [2]float64{l.Lng, l.Lat},
		"window_orientation": nonNilStrings(l.WindowOrientation),
		"description":        l.Description,
		"photos":             nonNilStrings(l.Photos),
		"source_url":         l.SourceURL,
		"import_error":       l.ImportError,
		"published_at":       l.PublishedAt,
		"updated_at":         l.UpdatedAt,
	}
}

// nonNilStrings превращает nil-срез в пустой массив: в JSON null и []
// различимы, и фронту приходится обрабатывать оба случая на ровном месте.
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func ownerPreviewDTO(p service.ImportPreview) fiber.Map {
	similar := make([]fiber.Map, 0, len(p.Similar))
	for _, s := range p.Similar {
		similar = append(similar, fiber.Map{
			"external_id": s.ExternalID, "address": s.Address,
			"price": s.Price, "area": s.Area,
		})
	}
	out := fiber.Map{
		"verdict": p.Verdict,
		"draft":   OwnerListingDTO(p.Draft),
		"similar": similar,
	}
	if p.ExistingID != nil {
		out["existing_id"] = *p.ExistingID
	}
	return out
}

func ownerListingID(c *fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("listing_id"))
	if err != nil {
		// Кривой uuid неотличим от несуществующего объявления: 404, не 400 —
		// тот же выбор, что сделан для чатов.
		return uuid.Nil, apperr.OwnerListingNotFound()
	}
	return id, nil
}

func (h *OwnerHandler) List(c *fiber.Ctx) error {
	items, err := h.listings.List(c.Context(), middleware.UserID(c))
	if err != nil {
		return err
	}
	out := make([]fiber.Map, 0, len(items))
	for _, l := range items {
		out = append(out, OwnerListingDTO(l))
	}
	return c.JSON(fiber.Map{"listings": out})
}

func (h *OwnerHandler) Get(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	l, err := h.listings.Get(c.Context(), middleware.UserID(c), id)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(l))
}

type ownerListingBody struct {
	City              *string   `json:"city"`
	Price             *int64    `json:"price"`
	Area              *float32  `json:"area"`
	KitchenArea       *float32  `json:"kitchen_area"`
	Rooms             *int      `json:"rooms"`
	Level             *int      `json:"level"`
	Levels            *int      `json:"levels"`
	Address           *string   `json:"address"`
	Coordinates       *[2]float64 `json:"coordinates"`
	WindowOrientation *[]string `json:"window_orientation"`
	Description       *string   `json:"description"`
}

func (h *OwnerHandler) Create(c *fiber.Ctx) error {
	var body ownerListingBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	if body.City == nil {
		return apperr.Validation("Не указан город")
	}
	if body.Coordinates == nil {
		return apperr.Validation("Не указаны координаты — поставьте точку на карте")
	}
	draft := domain.OwnerListing{
		City: *body.City, Price: body.Price, Area: body.Area,
		KitchenArea: body.KitchenArea, Rooms: body.Rooms,
		Level: body.Level, Levels: body.Levels,
		Lng: body.Coordinates[0], Lat: body.Coordinates[1],
	}
	if body.Address != nil {
		draft.Address = *body.Address
	}
	if body.Description != nil {
		draft.Description = *body.Description
	}
	if body.WindowOrientation != nil {
		draft.WindowOrientation = *body.WindowOrientation
	}
	created, err := h.listings.CreateManual(c.Context(), middleware.UserID(c), draft)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(OwnerListingDTO(created))
}

func (h *OwnerHandler) Update(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	var body ownerListingBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	fields := domain.OwnerListingFields{
		City: body.City, Price: body.Price, Area: body.Area,
		KitchenArea: body.KitchenArea, Rooms: body.Rooms,
		Level: body.Level, Levels: body.Levels, Address: body.Address,
		WindowOrientation: body.WindowOrientation, Description: body.Description,
	}
	if body.Coordinates != nil {
		lng, lat := body.Coordinates[0], body.Coordinates[1]
		fields.Lng, fields.Lat = &lng, &lat
	}
	updated, err := h.listings.Update(c.Context(), middleware.UserID(c), id, fields)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(updated))
}

type importBody struct {
	URL string `json:"url"`
}

func (h *OwnerHandler) ImportPreview(c *fiber.Ctx) error {
	var body importBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	preview, err := h.imports.Preview(c.Context(), middleware.UserID(c), body.URL)
	if err != nil {
		return err
	}
	return c.JSON(ownerPreviewDTO(preview))
}

// Import создаёт карточку и, если включена автопубликация, сразу отдаёт её
// витрине. Провал публикации не отменяет импорт: карточка остаётся в кабинете
// со статусом failed и кнопкой «Повторить» — терять уже забранные с Циана
// данные из-за недоступного ML нельзя.
func (h *OwnerHandler) Import(c *fiber.Ctx) error {
	var body importBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	userID := middleware.UserID(c)
	created, err := h.imports.Import(c.Context(), userID, body.URL)
	if err != nil {
		return err
	}
	if h.listings.Autopublish() && created.Status == "draft" {
		if published, pubErr := h.listings.Publish(c.Context(), userID, created.ID); pubErr == nil {
			created = published
		} else {
			created, _ = h.listings.Get(c.Context(), userID, created.ID)
		}
	}
	return c.Status(fiber.StatusCreated).JSON(OwnerListingDTO(created))
}

func (h *OwnerHandler) Publish(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	l, err := h.listings.Publish(c.Context(), middleware.UserID(c), id)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(l))
}

func (h *OwnerHandler) Unpublish(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	l, err := h.listings.Unpublish(c.Context(), middleware.UserID(c), id)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(l))
}

func (h *OwnerHandler) Delete(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	if err := h.listings.Delete(c.Context(), middleware.UserID(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
```

- [ ] **Step 4: Зарегистрировать роуты**

В `backend/internal/http/router.go`, в группу `/api/v1` после гео-роутов,
добавить (сигнатуру `RegisterRoutes` расширить параметром `owner *handlers.OwnerHandler`):

```go
	// Личный кабинет продавца. Всё за authMw: объявление всегда принадлежит
	// конкретному пользователю, анонимного доступа здесь нет по определению.
	ownerGroup := api.Group("/owner", authMw)
	ownerGroup.Get("/listings", owner.List)
	ownerGroup.Post("/listings", owner.Create)
	ownerGroup.Post("/listings/import/preview", owner.ImportPreview)
	ownerGroup.Post("/listings/import", owner.Import)
	ownerGroup.Get("/listings/:listing_id", owner.Get)
	ownerGroup.Patch("/listings/:listing_id", owner.Update)
	ownerGroup.Delete("/listings/:listing_id", owner.Delete)
	ownerGroup.Post("/listings/:listing_id/publish", owner.Publish)
	ownerGroup.Post("/listings/:listing_id/unpublish", owner.Unpublish)
```

**Порядок важен:** `/listings/import` объявляется до `/listings/:listing_id`,
иначе Fiber примет `import` за uuid и вернёт 404.

- [ ] **Step 5: Собрать зависимости в `main.go`**

В `backend/cmd/api/main.go`, рядом с остальной ручной проводкой:

```go
	ownerRepo := repository.NewOwnerListingRepo(pool)
	ownerListingService := service.NewOwnerListingService(
		ownerRepo,
		client.NewMLClient(cfg.MLServiceURL, time.Duration(cfg.MLOwnerTimeoutS)*time.Second),
		cfg.OwnerAutopublish,
	)

	// Сессия к Циану поднимается лениво, по первому импорту: на старте она не
	// нужна, а её создание ходит в сеть за cookie.
	offerFetcher := service.NewLazyOfferFetcher(cfg.CianProxies, cfg.CianRegion,
		time.Duration(cfg.MLSearchTimeoutS)*time.Second)
	ownerImportService := service.NewOwnerImportService(
		ownerRepo, listingRepo, offerFetcher,
		cian.NewRateLimiter(cfg.CianFetchPerMin, nil),
		cian.NewUserQuota(cfg.OwnerImportPerHour, nil),
	)
	ownerHandler := handlers.NewOwnerHandler(ownerListingService, ownerImportService)
```

и передать `ownerHandler` в `RegisterRoutes`. Имя переменной пула и
`listingRepo` взять фактические — они уже есть в файле.

- [ ] **Step 6: Написать ленивый фетчер**

Создать `backend/internal/service/offer_fetcher.go`:

```go
package service

import (
	"context"
	"errors"
	"sync"
	"time"

	rand "math/rand/v2"

	"habitus-backend/internal/cian"
)

// LazyOfferFetcher держит по одной сессии на прокси и создаёт их по требованию.
// Сессия обязана оставаться привязанной к своему прокси: Циан привязывает
// challenge-куки к IP, и смена прокси под живой сессией гарантирует блокировку.
type LazyOfferFetcher struct {
	mu       sync.Mutex
	proxies  []string
	region   int
	timeout  time.Duration
	sessions map[string]*cian.Session
}

func NewLazyOfferFetcher(proxies []string, region int, timeout time.Duration) *LazyOfferFetcher {
	return &LazyOfferFetcher{proxies: proxies, region: region, timeout: timeout,
		sessions: map[string]*cian.Session{}}
}

func (f *LazyOfferFetcher) FetchByID(ctx context.Context, offerID int64) (cian.Listing, error) {
	session, err := f.session()
	if err != nil {
		return cian.Listing{}, err
	}
	listing, err := session.FetchByID(ctx, offerID)
	if errors.Is(err, cian.ErrBlocked) {
		// Заблокированную сессию выбрасываем: следующий импорт поднимет новую,
		// с другим отпечатком браузера и, возможно, другим прокси.
		f.drop(session)
	}
	return listing, err
}

func (f *LazyOfferFetcher) session() (*cian.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := ""
	if len(f.proxies) > 0 {
		key = f.proxies[rand.IntN(len(f.proxies))]
	}
	if s, ok := f.sessions[key]; ok {
		return s, nil
	}
	s, err := cian.NewTLSSession(key, f.timeout, cian.SessionConfig{
		Region: f.region, BootstrapCookies: true,
	})
	if err != nil {
		return nil, err
	}
	f.sessions[key] = s
	return s, nil
}

func (f *LazyOfferFetcher) drop(target *cian.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, s := range f.sessions {
		if s == target {
			s.Close()
			delete(f.sessions, key)
			return
		}
	}
}
```

- [ ] **Step 7: Убедиться, что тесты проходят и приложение собирается**

Запустить: `cd backend && go build ./... && go vet ./... && go test ./...`
Ожидается: сборка без ошибок, 0 failed.

- [ ] **Step 8: Коммит**

```bash
git add backend/internal/http/handlers/owner_handler.go backend/internal/http/handlers/owner_handler_test.go \
        backend/internal/http/router.go backend/cmd/api/main.go backend/internal/service/offer_fetcher.go
git commit -m "feat: ручки личного кабинета продавца в шлюзе"
```

---

### Task 14: Загрузка и удаление фотографий

**Files:**
- Create: `backend/internal/service/photo_store.go`
- Modify: `backend/internal/http/handlers/owner_handler.go` (две ручки)
- Modify: `backend/internal/http/router.go` (два роута)
- Modify: `backend/cmd/api/main.go` (проводка хранилища)
- Modify: `backend/internal/app/app.go` (BodyLimit для загрузки)
- Test: `backend/internal/service/photo_store_test.go`

**Interfaces:**
- Consumes: `apperr` (Task 9), `service.OwnerListingService.SetPhotos` (Task 12)
- Produces:
  - `service.PhotoStore` со `NewPhotoStore(rootDir string, maxBytes int64, maxCount int) *PhotoStore`
  - `(*PhotoStore).Save(listingID uuid.UUID, filename string, r io.Reader, size int64) (string, error)` → публичный URL
  - `(*PhotoStore).Delete(listingID uuid.UUID, url string) error`
  - `(*PhotoStore).DeleteAll(listingID uuid.UUID) error`
  - `(*PhotoStore).MaxCount() int`
  - `handlers.(*OwnerHandler).UploadPhotos`, `handlers.(*OwnerHandler).DeletePhoto`

- [ ] **Step 1: Написать падающий тест**

Создать `backend/internal/service/photo_store_test.go`:

```go
package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
)

// Минимальные валидные заголовки: http.DetectContentType смотрит только на них.
var (
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0}, 600)...)
	pngBytes  = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0}, 600)...)
	gifBytes  = append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 600)...)
	textBytes = []byte(strings.Repeat("это не картинка ", 40))
)

func TestPhotoStoreSavesAndReturnsPublicURL(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()

	url, err := store.Save(listingID, "фото квартиры.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.HasPrefix(url, "/static/uploads/"+listingID.String()+"/") {
		t.Fatalf("url = %q", url)
	}
	if !strings.HasSuffix(url, ".jpg") {
		t.Fatalf("расширение должно выводиться из содержимого: %q", url)
	}
	// Имя файла клиента не должно попадать в путь: это вектор обхода каталога
	// и источник кракозябр в URL.
	if strings.Contains(url, "фото") {
		t.Fatalf("имя клиента протекло в путь: %q", url)
	}
	onDisk := filepath.Join(root, "uploads", listingID.String())
	entries, err := os.ReadDir(onDisk)
	if err != nil || len(entries) != 1 {
		t.Fatalf("файл не сохранён: %v, %d", err, len(entries))
	}
}

func TestPhotoStoreAcceptsPNG(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)
	url, err := store.Save(uuid.New(), "x.bin", bytes.NewReader(pngBytes), int64(len(pngBytes)))
	if err != nil {
		t.Fatalf("save png: %v", err)
	}
	if !strings.HasSuffix(url, ".png") {
		t.Fatalf("url = %q", url)
	}
}

func TestPhotoStoreRejectsByContentNotExtension(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)

	// Текст, переименованный в .jpg, — самый частый способ протащить не-картинку.
	_, err := store.Save(uuid.New(), "trojan.jpg", bytes.NewReader(textBytes), int64(len(textBytes)))

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "photo_unsupported_format" {
		t.Fatalf("ожидался photo_unsupported_format, получено %v", err)
	}
}

func TestPhotoStoreRejectsGIF(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)
	_, err := store.Save(uuid.New(), "anim.gif", bytes.NewReader(gifBytes), int64(len(gifBytes)))

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "photo_unsupported_format" {
		t.Fatalf("ожидался photo_unsupported_format, получено %v", err)
	}
}

func TestPhotoStoreRejectsOversize(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 100, 20)
	_, err := store.Save(uuid.New(), "big.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "photo_too_large" {
		t.Fatalf("ожидался photo_too_large, получено %v", err)
	}
}

func TestPhotoStoreDeleteRemovesOnlyOwnFile(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()
	first, _ := store.Save(listingID, "a.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))
	second, _ := store.Save(listingID, "b.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))

	if err := store.Delete(listingID, first); err != nil {
		t.Fatalf("delete: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "uploads", listingID.String()))
	if len(entries) != 1 {
		t.Fatalf("должен остаться один файл, осталось %d", len(entries))
	}
	if !strings.HasSuffix(second, entries[0].Name()) {
		t.Fatalf("удалён не тот файл")
	}
}

func TestPhotoStoreDeleteRefusesPathEscape(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()

	err := store.Delete(listingID, "/static/uploads/"+listingID.String()+"/../../../etc/passwd")
	if err == nil {
		t.Fatal("выход за пределы каталога объявления обязан отвергаться")
	}
}

func TestPhotoStoreDeleteIgnoresExternalURL(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)

	// Фото с CDN Циана не наши: удалять на диске нечего, и это не ошибка —
	// ссылка просто убирается из массива.
	if err := store.Delete(uuid.New(), "https://images.cdn-cian.ru/images/1.jpg"); err != nil {
		t.Fatalf("внешняя ссылка не должна быть ошибкой: %v", err)
	}
}

func TestPhotoStoreDeleteAll(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()
	_, _ = store.Save(listingID, "a.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))

	if err := store.DeleteAll(listingID); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "uploads", listingID.String())); !os.IsNotExist(err) {
		t.Fatal("каталог объявления должен быть удалён")
	}
}
```

- [ ] **Step 2: Убедиться, что тест не компилируется**

Запустить: `cd backend && go test ./internal/service/ -run TestPhotoStore -v`
Ожидается: FAIL — `undefined: NewPhotoStore`.

- [ ] **Step 3: Реализовать хранилище**

Создать `backend/internal/service/photo_store.go`:

```go
package service

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
)

const publicPhotoPrefix = "/static/uploads/"

// extByContentType — единственный источник правды о допустимых форматах.
// Тип определяется по сигнатуре файла, а не по расширению и не по заголовку
// клиента: и то и другое подделывается тривиально.
var extByContentType = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// PhotoStore кладёт фотографии объявления под StaticDir шлюза. Отдельное
// хранилище (S3 и т.п.) не нужно: app.Static уже раздаёт этот каталог.
type PhotoStore struct {
	rootDir  string
	maxBytes int64
	maxCount int
}

func NewPhotoStore(rootDir string, maxBytes int64, maxCount int) *PhotoStore {
	return &PhotoStore{rootDir: rootDir, maxBytes: maxBytes, maxCount: maxCount}
}

func (s *PhotoStore) MaxCount() int { return s.maxCount }

// Save записывает файл и возвращает публичный URL. Имя, присланное клиентом,
// не используется нигде: путь строится из uuid объявления и нового uuid файла.
func (s *PhotoStore) Save(listingID uuid.UUID, _ string, r io.Reader, size int64) (string, error) {
	if size > s.maxBytes {
		return "", apperr.PhotoTooLarge(int(s.maxBytes >> 20))
	}

	// Читаем целиком с запасом в один байт: так превышение лимита ловится даже
	// когда клиент соврал в Content-Length.
	data, err := io.ReadAll(io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > s.maxBytes {
		return "", apperr.PhotoTooLarge(int(s.maxBytes >> 20))
	}

	ext, ok := extByContentType[strings.Split(http.DetectContentType(data), ";")[0]]
	if !ok {
		return "", apperr.PhotoUnsupportedFormat()
	}

	dir := filepath.Join(s.rootDir, "uploads", listingID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := uuid.NewString() + ext
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return publicPhotoPrefix + listingID.String() + "/" + name, nil
}

// Delete удаляет наш файл. Ссылка на чужой CDN — не ошибка: у импортированного
// объявления фотографии остаются на стороне Циана, и удалять на диске нечего.
func (s *PhotoStore) Delete(listingID uuid.UUID, url string) error {
	if !strings.HasPrefix(url, publicPhotoPrefix) {
		return nil
	}
	rel := strings.TrimPrefix(url, publicPhotoPrefix)
	dir := filepath.Join(s.rootDir, "uploads", listingID.String())
	target := filepath.Join(s.rootDir, "uploads", filepath.Clean(rel))

	// filepath.Clean схлопывает ../, но результат всё равно надо проверить:
	// без этого «..» в имени выводит запись за пределы каталога объявления.
	if filepath.Dir(target) != dir {
		return errors.New("photo path escapes listing directory")
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *PhotoStore) DeleteAll(listingID uuid.UUID) error {
	return os.RemoveAll(filepath.Join(s.rootDir, "uploads", listingID.String()))
}
```

- [ ] **Step 4: Добавить ручки в хендлер**

В `backend/internal/http/handlers/owner_handler.go` расширить структуру и
конструктор полем `photos *service.PhotoStore`, затем дописать:

```go
func (h *OwnerHandler) UploadPhotos(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	userID := middleware.UserID(c)
	listing, err := h.listings.Get(c.Context(), userID, id)
	if err != nil {
		return err
	}

	form, err := c.MultipartForm()
	if err != nil {
		return apperr.Validation("Не удалось прочитать загруженные файлы")
	}
	files := form.File["photos"]
	if len(files) == 0 {
		return apperr.Validation("Не выбрано ни одной фотографии")
	}
	if len(listing.Photos)+len(files) > h.photos.MaxCount() {
		return apperr.PhotoLimitExceeded(h.photos.MaxCount())
	}

	urls := append([]string{}, listing.Photos...)
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			return apperr.Validation("Не удалось прочитать файл " + header.Filename)
		}
		url, saveErr := h.photos.Save(id, header.Filename, file, header.Size)
		_ = file.Close()
		if saveErr != nil {
			return saveErr
		}
		urls = append(urls, url)
	}

	updated, err := h.listings.SetPhotos(c.Context(), userID, id, urls)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(updated))
}

type deletePhotoBody struct {
	URL string `json:"url"`
}

func (h *OwnerHandler) DeletePhoto(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	var body deletePhotoBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	userID := middleware.UserID(c)
	listing, err := h.listings.Get(c.Context(), userID, id)
	if err != nil {
		return err
	}

	kept := make([]string, 0, len(listing.Photos))
	found := false
	for _, url := range listing.Photos {
		if url == body.URL {
			found = true
			continue
		}
		kept = append(kept, url)
	}
	if !found {
		return apperr.Validation("Такой фотографии нет в объявлении")
	}
	if err := h.photos.Delete(id, body.URL); err != nil {
		return apperr.Internal("Не удалось удалить фотографию")
	}

	updated, err := h.listings.SetPhotos(c.Context(), userID, id, kept)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(updated))
}
```

В `Delete` (Task 13) после успешного удаления карточки добавить очистку файлов:

```go
	if err := h.listings.Delete(c.Context(), middleware.UserID(c), id); err != nil {
		return err
	}
	// Файлы чистим после удаления карточки: осиротевший каталог безвреден,
	// а удалённые файлы при выжившей карточке дали бы битые ссылки.
	_ = h.photos.DeleteAll(id)
	return c.SendStatus(fiber.StatusNoContent)
```

- [ ] **Step 5: Роуты, лимит тела и проводка**

В `backend/internal/http/router.go` добавить в `ownerGroup`:

```go
	ownerGroup.Post("/listings/:listing_id/photos", owner.UploadPhotos)
	ownerGroup.Delete("/listings/:listing_id/photos", owner.DeletePhoto)
```

В `backend/cmd/api/main.go`:

```go
	photoStore := service.NewPhotoStore(cfg.StaticDir,
		int64(cfg.OwnerPhotoMaxMB)<<20, cfg.OwnerPhotoMaxCount)
	ownerHandler := handlers.NewOwnerHandler(ownerListingService, ownerImportService, photoStore)
```

В `backend/internal/app/app.go` поднять `BodyLimit`: текущий дефолт 1 МБ
(`BODY_LIMIT_BYTES`) меньше одной фотографии. Значение считать как
`max(cfg.BodyLimitBytes, (cfg.OwnerPhotoMaxMB+1)*cfg.OwnerPhotoMaxCount<<20)`
и оставить комментарий, почему предел определяется загрузкой фото, а не JSON.

- [ ] **Step 6: Убедиться, что тесты проходят**

Запустить: `cd backend && go build ./... && go test ./... -v -run 'TestPhotoStore|TestOwner'`
Ожидается: 0 failed.

- [ ] **Step 7: Прогнать весь Go-набор**

Запустить: `cd backend && go vet ./... && go test ./...`
Ожидается: 0 failed.

- [ ] **Step 8: Коммит**

```bash
git add backend/internal/service/photo_store.go backend/internal/service/photo_store_test.go \
        backend/internal/http/handlers/owner_handler.go backend/internal/http/router.go \
        backend/cmd/api/main.go backend/internal/app/app.go
git commit -m "feat: загрузка и удаление фотографий объявления"
```

- [ ] **Step 9: Дописать контракт в документацию**

В `frontend/Пайплайн фронт.md` добавить раздел «§7 Личный кабинет продавца» с
таблицей всех ручек `/api/v1/owner/*`, формой `OwnerListing`, вердиктами
предпросмотра (`new`, `claimable`, `already_yours`), полем `similar[]` и новыми
кодами ошибок. Это первичный источник по API — фронт (Task 15-18) читает его.

```bash
git add frontend/Пайплайн\ фронт.md
git commit -m "docs: контракт ручек личного кабинета продавца"
```

---

## Слой 5 — фронтенд кабинета

> **Для всех задач слоя 5:** перед написанием разметки вызвать скиллы
> `frontend-design` и `ui-ux-pro-max` — визуальное направление кабинета
> задаётся ими, а не подбирается по ходу. Существующие токены
> (`frontend/app/globals.css:21-36`) и шрифты Geist остаются; кабинет должен
> выглядеть частью того же продукта, что и рабочее пространство покупателя.
>
> **Запрещено:** счётчики просмотров, звонков, «популярности» и любые другие
> метрики, которых в системе нет. Правило проекта — не выдумывать факты.

### Task 15: Типы, API-клиент и примитивы UI

**Files:**
- Create: `frontend/lib/agent/owner.ts` (типы)
- Create: `frontend/lib/api/owner.ts` (клиент)
- Create: `frontend/components/ui/Button.tsx`, `Input.tsx`, `Select.tsx`,
  `Field.tsx`, `Card.tsx`, `Badge.tsx`, `Dialog.tsx`, `Toast.tsx`
- Create: `frontend/components/ui/index.ts`
- Test: `frontend/lib/api/owner.test.ts`, `frontend/components/ui/ui.test.tsx`

**Interfaces:**
- Consumes: `API_BASE` из `frontend/lib/api/config.ts`; конверт ошибок Go
- Produces:
  - Типы: `OwnerListing`, `OwnerListingStatus`, `ImportVerdict`,
    `ImportPreview`, `SimilarListing`, `OwnerListingDraft`
  - Клиент: `listOwnerListings`, `getOwnerListing`, `previewImport`,
    `importListing`, `createListing`, `updateListing`, `publishListing`,
    `unpublishListing`, `deleteListing`, `uploadPhotos`, `deletePhoto`,
    `OwnerApiError` (с полем `code`)
  - Примитивы UI с пропсами, описанными ниже
  Использует Task 16-18.

- [ ] **Step 1: Написать падающий тест клиента**

Создать `frontend/lib/api/owner.test.ts`:

```ts
import { afterEach, describe, expect, test, vi } from "vitest";
import { OwnerApiError, previewImport, listOwnerListings, uploadPhotos } from "./owner";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe("owner api", () => {
  test("previewImport передаёт ссылку и разбирает вердикт", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ verdict: "claimable", draft: { id: "1", status: "draft" }, similar: [] }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const preview = await previewImport("https://www.cian.ru/sale/flat/1/");

    expect(preview.verdict).toBe("claimable");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toContain("/owner/listings/import/preview");
    expect(init.credentials).toBe("include");
    expect(JSON.parse(init.body)).toEqual({ url: "https://www.cian.ru/sale/flat/1/" });
  });

  test("ошибка приходит с кодом, а не голым статусом", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      jsonResponse({ error: { code: "cian_unavailable", message: "Циан сейчас не отдаёт данные" } }, 503),
    ));

    await expect(previewImport("https://www.cian.ru/sale/flat/1/")).rejects.toMatchObject({
      code: "cian_unavailable",
      message: "Циан сейчас не отдаёт данные",
    });
  });

  test("ошибка без разбираемого тела не роняет клиент", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("upstream down", { status: 502 })));

    const error = await previewImport("https://www.cian.ru/sale/flat/1/").catch((e) => e);

    expect(error).toBeInstanceOf(OwnerApiError);
    expect(error.code).toBe("internal_error");
    expect(error.message.length).toBeGreaterThan(0);
  });

  test("список возвращает массив даже при пустом ответе", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ listings: null })));
    await expect(listOwnerListings()).resolves.toEqual([]);
  });

  test("загрузка фото уходит как multipart без ручного Content-Type", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ id: "1", photos: ["/static/uploads/1/a.jpg"] }));
    vi.stubGlobal("fetch", fetchMock);
    const file = new File([new Uint8Array([1, 2, 3])], "a.jpg", { type: "image/jpeg" });

    await uploadPhotos("1", [file]);

    const [, init] = fetchMock.mock.calls[0];
    expect(init.body).toBeInstanceOf(FormData);
    // Границу multipart проставляет браузер; заданный вручную заголовок её ломает.
    expect(init.headers?.["Content-Type"]).toBeUndefined();
  });
});
```

- [ ] **Step 2: Убедиться, что тест падает**

Запустить: `cd frontend && npx vitest run lib/api/owner.test.ts`
Ожидается: FAIL — `Failed to resolve import "./owner"`.

- [ ] **Step 3: Написать типы**

Создать `frontend/lib/agent/owner.ts`:

```ts
// Формы кабинета продавца. Зеркало Go-DTO (handlers.OwnerListingDTO) и
// pydantic-схем (habitus/online/schema.py) — три стороны держатся синхронно.

export type OwnerListingStatus =
  | "draft"
  | "publishing"
  | "published"
  | "unpublished"
  | "failed";

export type OwnerListingOrigin = "cian" | "manual";

export type ImportVerdict = "new" | "claimable" | "already_yours";

export interface OwnerListing {
  id: string;
  external_id: string;
  origin: OwnerListingOrigin;
  status: OwnerListingStatus;
  verification: "unverified" | "verified";
  city: "msk" | "spb";
  price: number | null;
  area: number | null;
  kitchen_area: number | null;
  rooms: number | null;
  level: number | null;
  levels: number | null;
  address: string;
  /** Всегда [lng, lat], WGS84 — как везде в проекте. */
  coordinates: [number, number];
  window_orientation: string[];
  description: string;
  photos: string[];
  source_url: string;
  import_error: string;
  published_at: string | null;
  updated_at: string;
}

export interface SimilarListing {
  external_id: string;
  address: string;
  price: number | null;
  area: number | null;
}

export interface ImportPreview {
  verdict: ImportVerdict;
  draft: OwnerListing;
  /** Похожие объекты ортогональны вердикту: новое объявление тоже может их иметь. */
  similar: SimilarListing[];
  existing_id?: string;
}

/** Поля, которые фронт отправляет на создание и правку. */
export interface OwnerListingDraft {
  city?: "msk" | "spb";
  price?: number | null;
  area?: number | null;
  kitchen_area?: number | null;
  rooms?: number | null;
  level?: number | null;
  levels?: number | null;
  address?: string;
  coordinates?: [number, number];
  window_orientation?: string[];
  description?: string;
}

export const STATUS_LABEL: Record<OwnerListingStatus, string> = {
  draft: "Черновик",
  publishing: "Публикуется",
  published: "Опубликовано",
  unpublished: "Снято с публикации",
  failed: "Ошибка публикации",
};
```

- [ ] **Step 4: Написать клиент**

Создать `frontend/lib/api/owner.ts`:

```ts
import { API_BASE } from "./config";
import type {
  ImportPreview,
  OwnerListing,
  OwnerListingDraft,
} from "@/lib/agent/owner";

/** Ошибка с кодом бэка: экраны разводят по коду, а не по тексту. */
export class OwnerApiError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "OwnerApiError";
    this.code = code;
  }
}

async function failure(res: Response): Promise<OwnerApiError> {
  try {
    const body = await res.json();
    const code = body?.error?.code ?? "internal_error";
    const message = body?.error?.message ?? "Что-то пошло не так";
    return new OwnerApiError(code, message);
  } catch {
    return new OwnerApiError("internal_error", "Сервис недоступен. Попробуйте позже");
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, { credentials: "include", ...init });
  if (!res.ok) throw await failure(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

function json(method: string, body?: unknown): RequestInit {
  return {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  };
}

export async function listOwnerListings(): Promise<OwnerListing[]> {
  const data = await request<{ listings: OwnerListing[] | null }>("/owner/listings");
  return data.listings ?? [];
}

export function getOwnerListing(id: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}`);
}

export function previewImport(url: string): Promise<ImportPreview> {
  return request<ImportPreview>("/owner/listings/import/preview", json("POST", { url }));
}

export function importListing(url: string): Promise<OwnerListing> {
  return request<OwnerListing>("/owner/listings/import", json("POST", { url }));
}

export function createListing(draft: OwnerListingDraft): Promise<OwnerListing> {
  return request<OwnerListing>("/owner/listings", json("POST", draft));
}

export function updateListing(id: string, draft: OwnerListingDraft): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}`, json("PATCH", draft));
}

export function publishListing(id: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}/publish`, json("POST"));
}

export function unpublishListing(id: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}/unpublish`, json("POST"));
}

export function deleteListing(id: string): Promise<void> {
  return request<void>(`/owner/listings/${id}`, { method: "DELETE" });
}

export function uploadPhotos(id: string, files: File[]): Promise<OwnerListing> {
  const form = new FormData();
  for (const file of files) form.append("photos", file);
  // Content-Type не задаём: границу multipart проставляет браузер.
  return request<OwnerListing>(`/owner/listings/${id}/photos`, { method: "POST", body: form });
}

export function deletePhoto(id: string, url: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}/photos`, json("DELETE", { url }));
}
```

- [ ] **Step 5: Проверить клиент**

Запустить: `cd frontend && npx vitest run lib/api/owner.test.ts`
Ожидается: 5 passed.

- [ ] **Step 6: Написать падающий тест примитивов**

Создать `frontend/components/ui/ui.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";
import { Badge, Button, Field, Input } from "./index";

test("Button в состоянии загрузки недоступен и объявляет это ассистивным технологиям", () => {
  render(<Button loading>Опубликовать</Button>);
  const button = screen.getByRole("button", { name: /опубликовать/i });
  expect(button).toBeDisabled();
  expect(button).toHaveAttribute("aria-busy", "true");
});

test("Button не срабатывает во время загрузки", async () => {
  const onClick = vi.fn();
  render(<Button loading onClick={onClick}>Опубликовать</Button>);
  await userEvent.click(screen.getByRole("button"));
  expect(onClick).not.toHaveBeenCalled();
});

test("Field связывает лейбл, подсказку и ошибку с полем", () => {
  render(
    <Field label="Цена" hint="В рублях" error="Укажите цену">
      <Input />
    </Field>,
  );
  const input = screen.getByLabelText("Цена");
  expect(input).toHaveAttribute("aria-invalid", "true");
  const describedBy = input.getAttribute("aria-describedby") ?? "";
  expect(describedBy.split(" ").length).toBe(2);
  expect(screen.getByText("Укажите цену")).toBeInTheDocument();
  expect(screen.getByText("В рублях")).toBeInTheDocument();
});

test("Field без ошибки не помечает поле невалидным", () => {
  render(
    <Field label="Площадь">
      <Input />
    </Field>,
  );
  expect(screen.getByLabelText("Площадь")).not.toHaveAttribute("aria-invalid", "true");
});

test("Badge статуса читается текстом, а не только цветом", () => {
  render(<Badge tone="warn">Черновик</Badge>);
  expect(screen.getByText("Черновик")).toBeInTheDocument();
});
```

- [ ] **Step 7: Написать примитивы**

Создать восемь файлов в `frontend/components/ui/` и барель `index.ts`,
реэкспортирующий их все. Требования к каждому:

- **`Button.tsx`** — пропсы `variant?: "primary" | "secondary" | "ghost" | "danger"`,
  `loading?: boolean`, остальное — нативные пропсы `<button>`. При `loading`:
  `disabled`, `aria-busy="true"`, спиннер вместо текста не ставить (текст должен
  оставаться читаемым для скринридера).
- **`Input.tsx`** — обёртка над `<input>` c `forwardRef`, базовые классы взять из
  константы `field` в `frontend/components/auth/AuthGate.tsx:10-11`, чтобы вход и
  кабинет выглядели одинаково; после переноса заменить там локальную константу
  на `<Input>`.
- **`Select.tsx`** — обёртка над `<select>` с тем же оформлением.
- **`Field.tsx`** — лейбл, подсказка, ошибка. Генерирует id через `useId`,
  клонирует единственного ребёнка, проставляя ему `id`, `aria-describedby`
  (подсказка и ошибка) и `aria-invalid` при наличии ошибки.
- **`Card.tsx`** — контейнер с рамкой и радиусом; пропсы `as?`, `className`.
- **`Badge.tsx`** — пропс `tone?: "neutral" | "ok" | "warn" | "danger"`.
  Цвета брать из `frontend/lib/grade.ts:1-6`, чтобы палитра статусов совпадала
  с палитрой оценок.
- **`Dialog.tsx`** — модальное окно на нативном `<dialog>`: закрытие по Esc и
  клику вне, `aria-labelledby` на заголовок, возврат фокуса на элемент-источник.
- **`Toast.tsx`** — контейнер `role="status"` с `aria-live="polite"` и хук
  `useToast()`; сообщения об успехе и ошибке кабинет показывает им.

Все примитивы уважают `prefers-reduced-motion` (в `globals.css:193-195` правило
уже есть) и глобальный `:focus-visible`.

- [ ] **Step 8: Проверить примитивы**

Запустить: `cd frontend && npx vitest run components/ui/ui.test.tsx`
Ожидается: 5 passed.

- [ ] **Step 9: Прогнать весь фронтовый набор**

Запустить: `cd frontend && npm test && npx tsc --noEmit && npm run lint`
Ожидается: 0 failed, 0 ошибок типов, 0 ошибок линтера.

- [ ] **Step 10: Коммит**

```bash
git add frontend/lib/agent/owner.ts frontend/lib/api/owner.ts frontend/components/ui/ frontend/components/auth/AuthGate.tsx
git commit -m "feat: типы, API-клиент и примитивы UI для кабинета продавца"
```

---

### Task 16: Роутинг кабинета, список объявлений и профиль

**Files:**
- Modify: `frontend/app/page.tsx` (убрать локальный `AuthGate`)
- Create: `frontend/app/(app)/layout.tsx` — если группировка роутов
  усложняет структуру, допустимо оставить `app/layout.tsx` и обернуть в
  `AuthGate` там; выбрать один вариант и держаться его
- Create: `frontend/app/lk/layout.tsx`, `frontend/app/lk/page.tsx`,
  `frontend/app/lk/profile/page.tsx`
- Create: `frontend/components/owner/CabinetShell.tsx`,
  `frontend/components/owner/ListingsList.tsx`,
  `frontend/components/owner/ListingRow.tsx`,
  `frontend/components/owner/StatusBadge.tsx`,
  `frontend/components/owner/EmptyCabinet.tsx`,
  `frontend/components/owner/ProfileCard.tsx`
- Modify: `frontend/components/shell/LeftRail.tsx:55-71` (кнопка «Кабинет»)
- Test: `frontend/components/owner/ListingsList.test.tsx`,
  `frontend/components/owner/ProfileCard.test.tsx`

**Interfaces:**
- Consumes: `listOwnerListings`, `deleteListing`, `unpublishListing`,
  `publishListing` (Task 15); `me`, `logout` из `frontend/lib/api/auth.ts`;
  примитивы `components/ui`
- Produces:
  - `CabinetShell` — общий каркас кабинета (шапка, навигация, слот контента)
  - `ListingsList({ listings, onChanged })`
  - `StatusBadge({ status })`
  - Роуты `/lk` и `/lk/profile`
  Использует Task 17-18.

- [ ] **Step 1: Написать падающий тест списка**

Создать `frontend/components/owner/ListingsList.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import type { OwnerListing } from "@/lib/agent/owner";
import ListingsList from "./ListingsList";

function listing(over: Partial<OwnerListing> = {}): OwnerListing {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    external_id: "cian_318394906",
    origin: "cian",
    status: "published",
    verification: "unverified",
    city: "msk",
    price: 12500000,
    area: 54.3,
    kitchen_area: null,
    rooms: 2,
    level: 4,
    levels: 17,
    address: "Москва, улица Мельникова, 3к1",
    coordinates: [37.6595, 55.7108],
    window_orientation: [],
    description: "Тихая двушка",
    photos: [],
    source_url: "https://www.cian.ru/sale/flat/318394906/",
    import_error: "",
    published_at: "2026-08-23T10:00:00Z",
    updated_at: "2026-08-23T10:00:00Z",
    ...over,
  };
}

test("показывает адрес, цену и статус", () => {
  render(<ListingsList listings={[listing()]} onChanged={vi.fn()} />);
  expect(screen.getByText(/Мельникова/)).toBeInTheDocument();
  expect(screen.getByText("Опубликовано")).toBeInTheDocument();
});

test("не рисует выдуманных метрик", () => {
  render(<ListingsList listings={[listing()]} onChanged={vi.fn()} />);
  // Счётчиков просмотров и звонков в системе нет — их не должно быть и в UI.
  expect(screen.queryByText(/просмотр/i)).toBeNull();
  expect(screen.queryByText(/звонк/i)).toBeNull();
});

test("незаполненную цену показывает прочерком, а не нулём", () => {
  render(<ListingsList listings={[listing({ price: null, status: "draft" })]} onChanged={vi.fn()} />);
  expect(screen.queryByText(/0\s*₽/)).toBeNull();
  expect(screen.getByText("Черновик")).toBeInTheDocument();
});

test("у объявления с ошибкой публикации видна причина и кнопка повтора", () => {
  render(
    <ListingsList
      listings={[listing({ status: "failed", import_error: "Витрина не приняла объявление" })]}
      onChanged={vi.fn()}
    />,
  );
  expect(screen.getByText(/Витрина не приняла объявление/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /повторить/i })).toBeInTheDocument();
});

test("каждая карточка ведёт на свою страницу", () => {
  render(<ListingsList listings={[listing()]} onChanged={vi.fn()} />);
  expect(screen.getByRole("link", { name: /Мельникова/ })).toHaveAttribute(
    "href",
    "/lk/listings/11111111-1111-1111-1111-111111111111",
  );
});
```

- [ ] **Step 2: Убедиться, что тест падает**

Запустить: `cd frontend && npx vitest run components/owner/ListingsList.test.tsx`
Ожидается: FAIL — `Failed to resolve import "./ListingsList"`.

- [ ] **Step 3: Поднять AuthGate в общий лэйаут**

Сейчас `AuthGate` живёт внутри `app/page.tsx` и потому покрывает только `/`.
Перенести его в лэйаут, чтобы он покрывал и `/lk`:

`frontend/app/layout.tsx` — обернуть `{children}` в `<AuthGate>`:

```tsx
import AuthGate from "@/components/auth/AuthGate";

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ru" className={`${GeistSans.variable} ${GeistMono.variable}`}>
      <body>
        <AuthGate>{children}</AuthGate>
      </body>
    </html>
  );
}
```

`frontend/app/page.tsx` — оставить только шелл:

```tsx
import AppShell from "@/components/shell/AppShell";

export default function Page() {
  return <AppShell />;
}
```

- [ ] **Step 4: Написать каркас и экраны**

Создать компоненты:

- **`CabinetShell.tsx`** — шапка с названием «Мои объявления», ссылкой на
  рабочее пространство (`/`), ссылкой на профиль (`/lk/profile`) и слотом
  контента. Никакого дублирования `LeftRail`: кабинет — отдельный контекст.
- **`StatusBadge.tsx`** — `<Badge>` с текстом из `STATUS_LABEL` и тоном:
  `published` → `ok`, `failed` → `danger`, `publishing` → `neutral`,
  `draft`/`unpublished` → `warn`.
- **`ListingRow.tsx`** — карточка одного объявления: обложка (первое фото или
  нейтральная заглушка без текста), адрес ссылкой на `/lk/listings/<id>`,
  цена через `money` из `frontend/lib/format.ts` (при `null` — «Цена не указана»),
  строка «2 комн · 54,3 м² · 4/17 этаж» из непустых полей, `StatusBadge`,
  дата обновления. Для `failed` — текст `import_error` и кнопка «Повторить»
  (`publishListing`). Для `published` — «Снять с публикации». Для остальных —
  «Опубликовать». Плюс «Удалить» через `Dialog` с подтверждением.
- **`EmptyCabinet.tsx`** — главное действие первым: крупное поле «Вставьте
  ссылку с Циана» (ведёт на `/lk/import` с подставленным значением), под ним
  неприметная ссылка «или заполнить вручную» на `/lk/new`.
- **`ListingsList.tsx`** — рендер списка `ListingRow`; пустой список отдаёт
  `EmptyCabinet`.
- **`ProfileCard.tsx`** — имя, email из `me()`, кнопка «Выйти», зовущая
  `logout()` и делающая `location.assign("/")`. Это первая точка вызова
  `logout()` в проекте.

Страницы:

- **`app/lk/layout.tsx`** — оборачивает детей в `CabinetShell`.
- **`app/lk/page.tsx`** — клиентский компонент: грузит `listOwnerListings()`,
  показывает состояние загрузки, ошибку с кнопкой «Повторить», иначе
  `ListingsList`.
- **`app/lk/profile/page.tsx`** — `ProfileCard`.

В `frontend/components/shell/LeftRail.tsx` заменить статичный аватар
(строки 66-71) на ссылку `/lk` с `aria-label="Личный кабинет"`; кнопку
«Настройки» без обработчика (55-64) удалить — мёртвый контрол.

- [ ] **Step 5: Написать тест профиля**

Создать `frontend/components/owner/ProfileCard.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";
import ProfileCard from "./ProfileCard";

vi.mock("@/lib/api/auth", () => ({
  logout: vi.fn().mockResolvedValue(undefined),
}));

test("показывает имя и почту и умеет выходить", async () => {
  const { logout } = await import("@/lib/api/auth");
  render(<ProfileCard user={{ id: "1", email: "seller@example.com", name: "Продавец" }} />);

  expect(screen.getByText("Продавец")).toBeInTheDocument();
  expect(screen.getByText("seller@example.com")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: /выйти/i }));
  expect(logout).toHaveBeenCalled();
});
```

- [ ] **Step 6: Убедиться, что тесты проходят**

Запустить: `cd frontend && npx vitest run components/owner/`
Ожидается: 6 passed.

- [ ] **Step 7: Проверить, что рабочее пространство не сломалось**

Запустить: `cd frontend && npm test && npx tsc --noEmit && npm run build`
Ожидается: 0 failed, сборка проходит. Особое внимание — тесты `AppShell` и
`a11y.test.tsx`: `LeftRail` изменился.

- [ ] **Step 8: Коммит**

```bash
git add frontend/app/ frontend/components/owner/ frontend/components/shell/LeftRail.tsx
git commit -m "feat: каркас кабинета, список объявлений и профиль продавца"
```

---

### Task 17: Экран импорта с Циана

**Files:**
- Create: `frontend/app/lk/import/page.tsx`
- Create: `frontend/components/owner/ImportForm.tsx`,
  `frontend/components/owner/ImportPreviewCard.tsx`,
  `frontend/components/owner/SimilarWarning.tsx`,
  `frontend/components/owner/CianUnavailable.tsx`
- Test: `frontend/components/owner/ImportForm.test.tsx`

**Interfaces:**
- Consumes: `previewImport`, `importListing`, `OwnerApiError` (Task 15);
  `ImportPreview`, `ImportVerdict` (Task 15); примитивы UI
- Produces: `ImportForm` — экран целиком, включая все состояния

- [ ] **Step 1: Написать падающий тест**

Создать `frontend/components/owner/ImportForm.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import type { ImportPreview, OwnerListing } from "@/lib/agent/owner";
import { OwnerApiError } from "@/lib/api/owner";
import ImportForm from "./ImportForm";

const previewImport = vi.fn();
const importListing = vi.fn();
const push = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    previewImport: (...args: unknown[]) => previewImport(...args),
    importListing: (...args: unknown[]) => importListing(...args),
  };
});

vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));

function draft(over: Partial<OwnerListing> = {}): OwnerListing {
  return {
    id: "1", external_id: "cian_318394906", origin: "cian", status: "draft",
    verification: "unverified", city: "msk", price: 12500000, area: 54.3,
    kitchen_area: null, rooms: 2, level: 4, levels: 17,
    address: "Москва, улица Мельникова, 3к1", coordinates: [37.6595, 55.7108],
    window_orientation: [], description: "", photos: [],
    source_url: "https://www.cian.ru/sale/flat/318394906/", import_error: "",
    published_at: null, updated_at: "2026-08-23T10:00:00Z", ...over,
  };
}

function preview(over: Partial<ImportPreview> = {}): ImportPreview {
  return { verdict: "new", draft: draft(), similar: [], ...over };
}

async function submit(url = "https://www.cian.ru/sale/flat/318394906/") {
  await userEvent.type(screen.getByLabelText(/ссылк/i), url);
  await userEvent.click(screen.getByRole("button", { name: /проверить|найти/i }));
}

beforeEach(() => {
  previewImport.mockReset();
  importListing.mockReset();
  push.mockReset();
});

test("вердикт new предлагает импортировать", async () => {
  previewImport.mockResolvedValue(preview());
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/Мельникова/)).toBeInTheDocument());
  expect(screen.getByRole("button", { name: /импортировать/i })).toBeInTheDocument();
});

test("вердикт claimable предлагает забрать уже известный объект", async () => {
  previewImport.mockResolvedValue(preview({ verdict: "claimable" }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/уже знаем эту квартиру/i)).toBeInTheDocument());
  expect(screen.getByRole("button", { name: /это моя квартира/i })).toBeInTheDocument();
});

test("вердикт already_yours ведёт на существующую карточку", async () => {
  previewImport.mockResolvedValue(preview({ verdict: "already_yours", existing_id: "42" }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/уже в вашем кабинете/i)).toBeInTheDocument());
  await userEvent.click(screen.getByRole("link", { name: /открыть/i }));
  expect(screen.getByRole("link", { name: /открыть/i })).toHaveAttribute("href", "/lk/listings/42");
});

test("похожий объект показывается предупреждением и не блокирует импорт", async () => {
  previewImport.mockResolvedValue(preview({
    similar: [{ external_id: "cian_777", address: "Мельникова 3к1", price: 12000000, area: 54 }],
  }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/похоже/i));
  expect(screen.getByRole("button", { name: /импортировать/i })).toBeEnabled();
});

test("недоступность Циана объясняется и уводит в ручную форму", async () => {
  previewImport.mockRejectedValue(new OwnerApiError("cian_unavailable", "Циан сейчас не отдаёт данные"));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/не отдаёт данные/i)).toBeInTheDocument());
  expect(screen.getByRole("link", { name: /заполнить вручную/i })).toHaveAttribute(
    "href",
    expect.stringContaining("/lk/new"),
  );
});

test("чужое объявление объясняется, а не падает молча", async () => {
  previewImport.mockRejectedValue(
    new OwnerApiError("listing_claimed_by_other", "Это объявление уже привязано к другому аккаунту"),
  );
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/другому аккаунту/i));
  expect(screen.queryByRole("button", { name: /импортировать/i })).toBeNull();
});

test("кривая ссылка ловится до запроса", async () => {
  previewImport.mockRejectedValue(new OwnerApiError("cian_url_invalid", "Это не похоже на ссылку"));
  render(<ImportForm />);
  await submit("моя квартира");

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/не похоже на ссылку/i));
});

test("успешный импорт уводит на карточку", async () => {
  previewImport.mockResolvedValue(preview());
  importListing.mockResolvedValue(draft({ id: "99", status: "published" }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => screen.getByRole("button", { name: /импортировать/i }));
  await userEvent.click(screen.getByRole("button", { name: /импортировать/i }));

  await waitFor(() => expect(push).toHaveBeenCalledWith("/lk/listings/99"));
});
```

- [ ] **Step 2: Убедиться, что тест падает**

Запустить: `cd frontend && npx vitest run components/owner/ImportForm.test.tsx`
Ожидается: FAIL — `Failed to resolve import "./ImportForm"`.

- [ ] **Step 3: Реализовать экран**

- **`ImportForm.tsx`** — поле ссылки в `<Field label="Ссылка на объявление
  с Циана">`, кнопка «Проверить». Состояния: `idle` → `checking` → один из
  четырёх исходов. Ошибки разводятся **по коду**: `cian_unavailable` →
  `CianUnavailable`, `listing_claimed_by_other` / `cian_url_invalid` /
  `cian_offer_not_found` / `rate_limited` → `role="alert"` с текстом бэка.
  Начальное значение поля читается из query-параметра `url` — так работает
  переход «Вставьте ссылку» из пустого состояния кабинета.
- **`ImportPreviewCard.tsx`** — карточка данных плюс действие, зависящее от
  вердикта: `new` → «Импортировать», `claimable` → заголовок «Мы уже знаем эту
  квартиру» и «Это моя квартира», `already_yours` → «Объявление уже в вашем
  кабинете» и ссылка «Открыть» на `/lk/listings/<existing_id>`.
- **`SimilarWarning.tsx`** — `role="alert"` с текстом «Похоже, эта квартира уже
  есть в базе» и списком найденных адресов. Рисуется **поверх любого вердикта**
  и никогда не отключает кнопку действия.
- **`CianUnavailable.tsx`** — объяснение и ссылка «Заполнить вручную» на
  `/lk/new?url=<исходная ссылка>`.

Страница `app/lk/import/page.tsx` — клиентский компонент, рендерит `ImportForm`.

- [ ] **Step 4: Убедиться, что тесты проходят**

Запустить: `cd frontend && npx vitest run components/owner/ImportForm.test.tsx`
Ожидается: 8 passed.

- [ ] **Step 5: Прогнать весь фронтовый набор**

Запустить: `cd frontend && npm test && npx tsc --noEmit && npm run build`
Ожидается: 0 failed.

- [ ] **Step 6: Коммит**

```bash
git add frontend/app/lk/import/ frontend/components/owner/
git commit -m "feat: экран импорта объявления с Циана"
```

---

### Task 18: Мастер создания и карточка объявления

**Files:**
- Create: `frontend/app/lk/new/page.tsx`, `frontend/app/lk/listings/[id]/page.tsx`
- Create: `frontend/components/owner/CreateWizard.tsx`,
  `frontend/components/owner/steps/LocationStep.tsx`,
  `frontend/components/owner/steps/ParamsStep.tsx`,
  `frontend/components/owner/steps/PhotosStep.tsx`,
  `frontend/components/owner/steps/PriceStep.tsx`,
  `frontend/components/owner/PinMap.tsx`,
  `frontend/components/owner/PhotoUploader.tsx`,
  `frontend/components/owner/ListingEditor.tsx`,
  `frontend/components/owner/ListingPreview.tsx`
- Test: `frontend/components/owner/CreateWizard.test.tsx`,
  `frontend/components/owner/PhotoUploader.test.tsx`,
  `frontend/components/owner/ListingEditor.test.tsx`

**Interfaces:**
- Consumes: `createListing`, `updateListing`, `uploadPhotos`, `deletePhoto`,
  `publishListing`, `unpublishListing`, `deleteListing`, `getOwnerListing`
  (Task 15); `useMaplibre` из `frontend/lib/map/useMaplibre.ts`; примитивы UI
- Produces: экраны `/lk/new` и `/lk/listings/[id]`

- [ ] **Step 1: Написать падающий тест мастера**

Создать `frontend/components/owner/CreateWizard.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import CreateWizard from "./CreateWizard";

const createListing = vi.fn();
const updateListing = vi.fn();
const push = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    createListing: (...a: unknown[]) => createListing(...a),
    updateListing: (...a: unknown[]) => updateListing(...a),
  };
});
vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));
// Карта требует WebGL, которого в jsdom нет: подменяем на кнопку, ставящую точку.
vi.mock("./PinMap", () => ({
  default: ({ onPick }: { onPick: (c: [number, number]) => void }) => (
    <button type="button" onClick={() => onPick([37.6055, 55.7601])}>
      поставить точку
    </button>
  ),
}));

beforeEach(() => {
  createListing.mockReset().mockResolvedValue({ id: "77", photos: [], status: "draft" });
  updateListing.mockReset().mockResolvedValue({ id: "77", photos: [], status: "draft" });
  push.mockReset();
});

test("без точки на карте дальше не пускает", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  expect(screen.getByRole("alert")).toHaveTextContent(/точк/i);
  expect(createListing).not.toHaveBeenCalled();
});

test("первый переход создаёт черновик, чтобы работа не терялась", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /поставить точку/i }));
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  await waitFor(() => expect(createListing).toHaveBeenCalledTimes(1));
  const draft = createListing.mock.calls[0][0];
  // Контракт координат единый по всему проекту: [lng, lat].
  expect(draft.coordinates).toEqual([37.6055, 55.7601]);
  expect(draft.city).toBe("msk");
});

test("шаги идут в понятном порядке и назад возвращает", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /поставить точку/i }));
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  await waitFor(() => expect(screen.getByText(/шаг 2/i)).toBeInTheDocument());
  await userEvent.click(screen.getByRole("button", { name: /назад/i }));
  expect(screen.getByText(/шаг 1/i)).toBeInTheDocument();
});

test("город определяется по поставленной точке, а не спрашивается", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /поставить точку/i }));
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  await waitFor(() => expect(createListing).toHaveBeenCalled());
  expect(screen.queryByLabelText(/выберите город/i)).toBeNull();
});
```

- [ ] **Step 2: Написать падающий тест загрузчика фото**

Создать `frontend/components/owner/PhotoUploader.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { OwnerApiError } from "@/lib/api/owner";
import PhotoUploader from "./PhotoUploader";

const uploadPhotos = vi.fn();
const deletePhoto = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    uploadPhotos: (...a: unknown[]) => uploadPhotos(...a),
    deletePhoto: (...a: unknown[]) => deletePhoto(...a),
  };
});

const jpeg = () => new File([new Uint8Array([255, 216, 255, 224])], "a.jpg", { type: "image/jpeg" });

beforeEach(() => {
  uploadPhotos.mockReset();
  deletePhoto.mockReset();
});

test("загружает выбранные файлы и показывает их", async () => {
  uploadPhotos.mockResolvedValue({ photos: ["/static/uploads/1/a.jpg"] });
  render(<PhotoUploader listingId="1" photos={[]} onChange={vi.fn()} />);

  await userEvent.upload(screen.getByLabelText(/добавить фото/i), jpeg());

  await waitFor(() => expect(uploadPhotos).toHaveBeenCalledWith("1", [expect.any(File)]));
});

test("отказ бэка показывается текстом, а не молча теряется", async () => {
  uploadPhotos.mockRejectedValue(new OwnerApiError("photo_too_large", "Фотография больше 10 МБ"));
  render(<PhotoUploader listingId="1" photos={[]} onChange={vi.fn()} />);

  await userEvent.upload(screen.getByLabelText(/добавить фото/i), jpeg());

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/больше 10 МБ/));
});

test("у каждого фото есть доступное имя и кнопка удаления", async () => {
  deletePhoto.mockResolvedValue({ photos: [] });
  render(
    <PhotoUploader listingId="1" photos={["/static/uploads/1/a.jpg"]} onChange={vi.fn()} />,
  );

  await userEvent.click(screen.getByRole("button", { name: /удалить фото 1/i }));
  await waitFor(() => expect(deletePhoto).toHaveBeenCalledWith("1", "/static/uploads/1/a.jpg"));
});
```

- [ ] **Step 3: Написать падающий тест карточки**

Создать `frontend/components/owner/ListingEditor.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import type { OwnerListing } from "@/lib/agent/owner";
import ListingEditor from "./ListingEditor";

const updateListing = vi.fn();
const publishListing = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    updateListing: (...a: unknown[]) => updateListing(...a),
    publishListing: (...a: unknown[]) => publishListing(...a),
  };
});
vi.mock("./PinMap", () => ({ default: () => <div /> }));

function listing(over: Partial<OwnerListing> = {}): OwnerListing {
  return {
    id: "1", external_id: "cian_1", origin: "cian", status: "published",
    verification: "unverified", city: "msk", price: 12500000, area: 54.3,
    kitchen_area: null, rooms: 2, level: 4, levels: 17,
    address: "Москва, улица Мельникова, 3к1", coordinates: [37.6595, 55.7108],
    window_orientation: [], description: "Тихая двушка", photos: [],
    source_url: "", import_error: "", published_at: "2026-08-23T10:00:00Z",
    updated_at: "2026-08-23T10:00:00Z", ...over,
  };
}

beforeEach(() => {
  updateListing.mockReset().mockImplementation(async (_id, patch) => listing(patch));
  publishListing.mockReset().mockResolvedValue(listing());
});

test("правка цены уходит на бэк", async () => {
  render(<ListingEditor listing={listing()} />);
  const price = screen.getByLabelText(/цена/i);
  await userEvent.clear(price);
  await userEvent.type(price, "11000000");
  await userEvent.click(screen.getByRole("button", { name: /сохранить/i }));

  await waitFor(() =>
    expect(updateListing).toHaveBeenCalledWith("1", expect.objectContaining({ price: 11000000 })),
  );
});

test("непроверенное объявление честно помечено", () => {
  render(<ListingEditor listing={listing()} />);
  expect(screen.getByText(/не подтверждено/i)).toBeInTheDocument();
});

test("превью показывает то же, что увидит покупатель", () => {
  render(<ListingEditor listing={listing()} />);
  const preview = screen.getByTestId("listing-preview");
  expect(preview).toHaveTextContent("Мельникова");
  expect(preview).toHaveTextContent("54,3");
});

test("пустое поле сохраняется как отсутствующее, а не как ноль", async () => {
  render(<ListingEditor listing={listing({ kitchen_area: null })} />);
  await userEvent.click(screen.getByRole("button", { name: /сохранить/i }));

  await waitFor(() => expect(updateListing).toHaveBeenCalled());
  const patch = updateListing.mock.calls[0][1];
  expect(patch.kitchen_area === null || patch.kitchen_area === undefined).toBe(true);
});
```

- [ ] **Step 4: Убедиться, что все три теста падают**

Запустить: `cd frontend && npx vitest run components/owner/CreateWizard.test.tsx components/owner/PhotoUploader.test.tsx components/owner/ListingEditor.test.tsx`
Ожидается: FAIL — модули не найдены.

- [ ] **Step 5: Реализовать карту-пин**

**`PinMap.tsx`** — обёртка над `useMaplibre` (`frontend/lib/map/useMaplibre.ts`)
с одним маркером. Пропсы: `value?: [number, number]`, `onPick(c: [number, number])`,
`city?: "msk" | "spb"`. Клик по карте перемещает маркер и зовёт `onPick`.
Координаты наружу — строго `[lng, lat]`.

Точка на карте вместо геокодера — сознательный выбор: она точнее, мгновенна и
не тянет зависимость от Nominatim, у которого жёсткие лимиты. Адрес продавец
вводит строкой рядом, он идёт в текст объявления, а не в определение места.

- [ ] **Step 6: Реализовать мастер**

**`CreateWizard.tsx`** — четыре шага с индикатором «Шаг N из 4»:

1. `LocationStep` — `PinMap` + поле адреса. Без точки «Далее» не работает,
   ошибка выводится `role="alert"`.
2. `ParamsStep` — комнаты, площадь, площадь кухни, этаж, этажность, ориентация окон.
3. `PhotosStep` — `PhotoUploader`.
4. `PriceStep` — цена и описание, кнопка «Опубликовать».

Черновик создаётся `createListing` при первом переходе с шага 1 (координаты уже
есть) и дальше правится `updateListing` на каждом переходе — уход со страницы
не теряет работу. **Город определяется по координатам** (bbox Москвы и Питера
те же, что в `habitus/clean/normalize.py`), у продавца его не спрашиваем:
человек, ставящий точку на карте, уже сказал, где квартира.

Страница `app/lk/new/page.tsx` рендерит `CreateWizard`; query-параметр `url`
(приходит из `CianUnavailable`) показывается подсказкой «Импорт не удался,
заполните вручную».

- [ ] **Step 7: Реализовать загрузчик фото и карточку**

**`PhotoUploader.tsx`** — `<input type="file" multiple accept="image/*">` с
лейблом «Добавить фото», превью сеткой, кнопкой удаления у каждого
(`aria-label="Удалить фото N"`), ошибками из `OwnerApiError` в `role="alert"`.

**`ListingPreview.tsx`** — карточка `data-testid="listing-preview"` в том виде,
в каком объект увидит покупатель: обложка, адрес, цена, строка параметров.
Переиспользовать оформление `frontend/components/result/PropertyCard.tsx`, но
не сам компонент — у него другой источник данных (`Property`, не `OwnerListing`).

**`ListingEditor.tsx`** — двухколоночный экран: слева `ListingPreview`, справа
форма правки на примитивах `Field`. Числовые поля: пустая строка → `null`,
никогда не `0`. Шапка: `StatusBadge`, пометка «Не подтверждено» с пояснением
«Мы не проверяли, что объявление принадлежит вам», действия
«Опубликовать»/«Снять с публикации»/«Удалить». Для `failed` — `import_error`
и «Повторить».

Страница `app/lk/listings/[id]/page.tsx` — грузит `getOwnerListing(id)`,
состояния загрузки и ошибки, затем `ListingEditor`.

- [ ] **Step 8: Убедиться, что тесты проходят**

Запустить: `cd frontend && npx vitest run components/owner/`
Ожидается: 0 failed.

- [ ] **Step 9: Прогнать всё**

Запустить: `cd frontend && npm test && npx tsc --noEmit && npm run lint && npm run build`
Ожидается: 0 failed, сборка проходит.

- [ ] **Step 10: Коммит**

```bash
git add frontend/app/lk/ frontend/components/owner/
git commit -m "feat: мастер создания объявления и карточка с правкой"
```

---

## Приёмка

После Task 18 — сквозная проверка на поднятом стеке, а не по тестам.

- [ ] **Step 1: Поднять всё**

```bash
docker compose up -d
cd backend && go test ./... && cd ..
uv run pytest
cd frontend && npm test
```

- [ ] **Step 2: Проверить сценарий «объявление уже в базе»**

Взять `external_id` любой строки `listings` (`psql`: `SELECT external_id FROM
listings WHERE source='cian' LIMIT 1;`), собрать из него ссылку
`https://www.cian.ru/sale/flat/<id>/`, вставить в `/lk/import`.
Ожидается: вердикт `claimable`, карточка с данными, **ноль исходящих запросов
к Циану** (проверить по логам шлюза).

- [ ] **Step 3: Проверить пробник Циана**

С настроенным `CIAN_PROXIES` импортировать ссылку на объявление, которого нет
в базе. Ожидается: либо карточка с данными, либо честный `cian_unavailable` с
переходом в ручную форму. **Оба исхода — приемлемый результат приёмки**:
доступность Циана от нас не зависит, а деградация обязана работать.

- [ ] **Step 4: Проверить сквозной путь ручного объявления**

Создать объявление через `/lk/new`, опубликовать, затем найти его в рабочем
пространстве поиском по адресу или характерному слову описания. Открыть досье —
блоки должны строиться так же, как у спарсенных объектов.

- [ ] **Step 5: Проверить, что батч-пайплайн не ломает кабинет**

```bash
uv run habitus offline --csv listings.csv --source cian --no-osm
```

Ожидается: опубликованные объявления продавца остались `is_active = true`, их
правки не перезаписаны.

- [ ] **Step 6: Записать результат**

Создать `docs/notes/owner-cabinet-acceptance-2026-XX-XX.md` с фактическим
исходом каждого шага, особенно шага 3: работает ли импорт с Циана на практике,
с какими прокси и с какой частотой блокировок. Это единственный способ узнать,
жизнеспособна ли фича, — и следующему, кто её тронет, эта запись нужнее кода.

```bash
git add docs/notes/
git commit -m "docs: результаты приёмки личного кабинета продавца"
```
