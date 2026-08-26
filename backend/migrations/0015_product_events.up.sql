-- Журнал продуктовых событий: воронка от поиска до заявки. Технические
-- метрики (latency, degraded, 429) на вопрос «дошёл ли человек» не отвечают.
CREATE TABLE product_events (
    id          bigserial PRIMARY KEY,
    -- SET NULL, а не CASCADE: свипер вычищает брошенных гостей, но воронка
    -- за прошлый месяц от этого обнуляться не должна.
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    -- Признак гостя хранится копией: после удаления пользователя восстановить
    -- его по джойну будет уже не с чем.
    is_guest    boolean NOT NULL DEFAULT false,
    kind        text NOT NULL,
    -- Без FK на chats нарочно: chat_id тут — историческая метка, а не связь,
    -- которую нужно поддерживать (в отличие от favorites, где FK есть и
    -- сохранённое должно жить только пока жив чат). Удаление чата не должно
    -- ни обнулять, ни каскадом рвать строки append-only журнала.
    chat_id     uuid,
    external_id text,
    props       jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX product_events_kind_ix ON product_events (kind, created_at DESC);
CREATE INDEX product_events_user_ix ON product_events (user_id, created_at DESC);
