-- Flat "latest snapshot per object in this chat" — what GET /objects/{id} reads.
-- Upsert-latest-wins by design: lifestyle_analysis should reflect the chat's
-- current context, not a full history. external_id is not a real FK into the
-- Python-owned `listings` table (different ownership boundary) — validated in
-- Go application code instead, right after reading it back out of `listings`.
CREATE TABLE chat_search_results (
    chat_id       UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    external_id   TEXT NOT NULL,
    search_id     UUID NOT NULL REFERENCES chat_searches(id) ON DELETE CASCADE,
    price         BIGINT,
    area          REAL,
    rooms         INT,
    address_facts JSONB NOT NULL DEFAULT '{}',
    score         REAL NOT NULL,
    explanation   TEXT NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, external_id)
);
