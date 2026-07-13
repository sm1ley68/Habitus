-- One row per completed search stream. Deliberately stores parsed_query/relaxed/
-- degraded even though this pass's API responses don't surface them yet — so the
-- Н.1 addendum (relaxation[]/zone_rationale) follow-up can read this table instead
-- of requiring a new migration.
CREATE TABLE chat_searches (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id        UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    message_id     UUID REFERENCES messages(id),
    raw_query      TEXT NOT NULL,
    parsed_query   JSONB,
    relaxed        TEXT[] NOT NULL DEFAULT '{}',
    data_freshness TEXT,
    degraded       TEXT[] NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX chat_searches_chat_id_ix ON chat_searches(chat_id, created_at DESC);
