-- Откат уносит гостей: строк без email в схеме с NOT NULL быть не может.
DROP INDEX IF EXISTS users_stale_guests_ix;
DELETE FROM users WHERE is_guest;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_credentials_ck;
ALTER TABLE users DROP COLUMN is_guest;
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE users ALTER COLUMN email SET NOT NULL;
