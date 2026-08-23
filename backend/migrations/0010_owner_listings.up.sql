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
