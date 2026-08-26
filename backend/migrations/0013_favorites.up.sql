-- Сохранённые объекты. Переживают чат: до этого объект жил только в
-- chat_search_results, и закрытая вкладка означала потерю находки.
CREATE TABLE favorites (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Ссылки на listings нет: таблица Python-owned, и внешний ключ из
    -- Go-схемы связал бы две системы миграций. Пропавший из витрины объект
    -- просто не попадает в выдачу списка.
    external_id text NOT NULL,
    -- Откуда сохранён: с этим chat_id паспорт откроется с досье и процентом
    -- совпадения, без него — как «с карты». ON DELETE SET NULL, потому что
    -- удаление чата не должно уносить находку.
    chat_id     uuid REFERENCES chats(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, external_id)
);

CREATE INDEX favorites_user_ix ON favorites (user_id, created_at DESC);
