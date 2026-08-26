-- 0012 обещала комментарием, что снятое с публикации и удалённое объявление
-- не должно унести заявку из истории продавца, но FK был ON DELETE CASCADE —
-- обещание не выполнялось. Чиним поведение: listing_id становится nullable
-- со SET NULL, а address (как уже сделано с external_id) хранится копией на
-- момент отправки, чтобы осиротевшая заявка осталась читаемой без объявления.
ALTER TABLE leads DROP CONSTRAINT leads_listing_id_fkey;
ALTER TABLE leads ALTER COLUMN listing_id DROP NOT NULL;
ALTER TABLE leads ADD CONSTRAINT leads_listing_id_fkey
    FOREIGN KEY (listing_id) REFERENCES owner_listings(id) ON DELETE SET NULL;

ALTER TABLE leads ADD COLUMN address text NOT NULL DEFAULT '';

-- Бэкфилл для уже существующих заявок: на момент миграции их объявления ещё
-- живы, адрес есть откуда скопировать.
UPDATE leads l SET address = ol.address
FROM owner_listings ol
WHERE ol.id = l.listing_id;
