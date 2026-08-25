-- Гость — обычный пользователь без учётных данных. Отдельной таблицы нет
-- намеренно: так все FK (sessions, chats, owner_listings) продолжают
-- ссылаться на users без правок, а регистрация гостя становится UPDATE той
-- же строки — чаты и результаты поиска прилипают к аккаунту сами.
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE users ADD COLUMN is_guest boolean NOT NULL DEFAULT false;

-- Учётные данные обязательны ровно для зарегистрированных. Без этого
-- ослабление NOT NULL выше открыло бы дорогу аккаунту без пароля.
ALTER TABLE users ADD CONSTRAINT users_credentials_ck
    CHECK (is_guest OR (email IS NOT NULL AND password_hash IS NOT NULL));

-- Свипер ищет брошенных гостей по возрасту; без индекса это seq scan по всей
-- таблице пользователей на каждом проходе.
CREATE INDEX users_stale_guests_ix ON users (created_at) WHERE is_guest;
