ALTER TABLE leads DROP COLUMN address;

-- Вернуть NOT NULL нельзя, пока в таблице есть строки-сироты (объявление уже
-- удалено после SET NULL) — как и в 0011_guest_users.down.sql, откат уносит
-- записи, которые не вписываются в старую схему.
DELETE FROM leads WHERE listing_id IS NULL;
ALTER TABLE leads DROP CONSTRAINT leads_listing_id_fkey;
ALTER TABLE leads ALTER COLUMN listing_id SET NOT NULL;
ALTER TABLE leads ADD CONSTRAINT leads_listing_id_fkey
    FOREIGN KEY (listing_id) REFERENCES owner_listings(id) ON DELETE CASCADE;
