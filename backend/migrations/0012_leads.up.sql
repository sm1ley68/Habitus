-- Заявка покупателя продавцу. Контакт продавца при этом НЕ раскрывается:
-- наружу уходит только то, что покупатель сам о себе сообщил.
CREATE TABLE leads (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id  uuid NOT NULL REFERENCES owner_listings(id) ON DELETE CASCADE,
    -- seller_id денормализован из owner_listings.user_id: список заявок
    -- продавца — самый частый запрос кабинета, и join ради него на каждой
    -- странице не нужен. Владелец объявления не меняется.
    seller_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    buyer_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- external_id хранится копией: объявление могут снять и удалить, а заявка
    -- в истории продавца должна остаться читаемой.
    external_id text NOT NULL,
    name        text NOT NULL,
    contact     text NOT NULL,
    message     text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX leads_seller_ix ON leads (seller_id, created_at DESC);

-- Одна заявка на объявление от одного покупателя. Это и есть защита от
-- повторной отправки: отдельный рейт-лимитер тут был бы лишней деталью.
CREATE UNIQUE INDEX leads_buyer_listing_uq ON leads (buyer_id, listing_id);
