CREATE TABLE chats (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    city              TEXT NOT NULL CHECK (city IN ('spb','msk')),
    title             TEXT NOT NULL DEFAULT 'Новый поиск квартиры',
    stream_active     BOOLEAN NOT NULL DEFAULT false,
    stream_started_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX chats_user_id_ix ON chats(user_id);
