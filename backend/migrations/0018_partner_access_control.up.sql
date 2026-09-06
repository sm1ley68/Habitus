-- Контроль выдачи доступа к Partner API.
--
-- До этой миграции ключ мог выпустить любой, у кого есть DB_DSN, и никакого
-- следа от этого не оставалось. Здесь появляются три вещи, которых не хватало:
-- явное одобрение партнёра перед выпуском, список адресов, с которых ключ
-- работает, и журнал того, кто что выдал и почему.

-- Партнёр заводится в состоянии pending и начинает работать только после
-- явного одобрения. Раньше «завели» и «пустили в бой» было одним действием,
-- и шага, на котором кто-то принимает решение, просто не существовало.
ALTER TABLE partners DROP CONSTRAINT IF EXISTS partners_status_check;
ALTER TABLE partners ADD CONSTRAINT partners_status_check
    CHECK (status IN ('pending', 'active', 'suspended'));
ALTER TABLE partners ALTER COLUMN status SET DEFAULT 'pending';

-- Разрешённые адреса ключа. Пустой массив означает «откуда угодно» — это
-- законное состояние для партнёра с плавающими адресами, а не «доступа нет».
ALTER TABLE partner_api_keys ADD COLUMN allowed_ips text[] NOT NULL DEFAULT '{}';

-- Журнал административных действий. Append-only: строку отсюда не правят и
-- не удаляют — иначе журнал перестаёт быть журналом.
CREATE TABLE partner_admin_log (
    id         bigserial PRIMARY KEY,
    -- SET NULL, а не CASCADE: удалённый партнёр не должен уносить с собой
    -- запись о том, что ему когда-то выдали доступ.
    partner_id uuid REFERENCES partners(id) ON DELETE SET NULL,
    -- Копией, а не джойном: партнёра могут удалить, а знать, кому выдавали,
    -- нужно и после этого.
    slug       text NOT NULL DEFAULT '',
    action     text NOT NULL,
    -- Открытый префикс ключа, если действие про ключ. Секрета здесь нет и
    -- быть не может.
    key_prefix text NOT NULL DEFAULT '',
    -- Кто именно это сделал. Берётся из окружения оператора, а не из ключа:
    -- «выдал сервер» — бесполезная запись.
    operator   text NOT NULL,
    reason     text NOT NULL DEFAULT '',
    details    jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX partner_admin_log_partner_ix ON partner_admin_log (partner_id, created_at DESC);
CREATE INDEX partner_admin_log_at_ix ON partner_admin_log (created_at DESC);
