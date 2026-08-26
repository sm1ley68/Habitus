-- Оценка объекта в выдаче. Ключ включает chat_id: оценка всегда «этот объект
-- под ЭТОТ запрос», вне запроса она ничего не значит.
CREATE TABLE result_feedback (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id     uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    external_id text NOT NULL,
    verdict     text NOT NULL CHECK (verdict IN ('up', 'down')),
    reason      text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chat_id, external_id)
);

-- Разбор качества подбора идёт по объектам и вердиктам, а не по людям.
CREATE INDEX result_feedback_verdict_ix ON result_feedback (verdict, created_at DESC);
