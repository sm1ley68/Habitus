-- Partner API — B2B-контур поверх того же ядра, что и B2C.
--
-- Партнёр не заводит отдельной сущности «владелец объявлений»: у него есть
-- служебная строка в users, и весь уже существующий код кабинета продавца
-- (owner_listings, leads) работает для него без единой правки. Отдельная
-- таблица partners хранит ровно то, чего у пользователя нет: тариф, лимиты и
-- ключи.
CREATE TABLE partners (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- slug — человекочитаемый идентификатор в логах и в CLI; наружу не уходит
    -- как первичный ключ, но по нему партнёра ищет администратор.
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    -- Служебный аккаунт партнёра. RESTRICT, а не CASCADE: удаление
    -- пользователя, за которым висят объявления и заявки, должно быть
    -- осознанным действием, а не побочным эффектом.
    user_id    uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    -- Лимиты индивидуальные: у интегратора-агрегатора и у одного агентства
    -- разный профиль нагрузки. NULL означает «взять значение из конфига
    -- сервиса» — синтетический ноль здесь читался бы как «ничего нельзя».
    rate_limit_per_min integer CHECK (rate_limit_per_min IS NULL OR rate_limit_per_min > 0),
    llm_limit_per_hour integer CHECK (llm_limit_per_hour IS NULL OR llm_limit_per_hour > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Ключи API. Секрет не хранится: в базе только SHA-256, а prefix — открытая
-- часть, по которой ключ ищут и которую показывают в интерфейсе («hab_live_
-- a1b2c3…»). Показать сам ключ второй раз невозможно by design.
CREATE TABLE partner_api_keys (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id  uuid NOT NULL REFERENCES partners(id) ON DELETE CASCADE,
    name        text NOT NULL DEFAULT '',
    -- Открытая часть ключа, она же индекс поиска. UNIQUE — коллизия prefix'ов
    -- сделала бы поиск ключа неоднозначным.
    prefix      text NOT NULL UNIQUE,
    secret_hash text NOT NULL,
    -- Среда ключа: live ходит в боевые данные, test — тот же контур с
    -- пометкой, по которой партнёр отличает свои прогоны от продакшена.
    environment text NOT NULL DEFAULT 'live' CHECK (environment IN ('live', 'test')),
    scopes      text[] NOT NULL DEFAULT '{}',
    -- Отзыв — не удаление: журнал должен помнить, каким ключом ходили.
    revoked_at   timestamptz,
    expires_at   timestamptz,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX partner_api_keys_partner_ix ON partner_api_keys (partner_id, created_at DESC);

-- Idempotency-Key: повтор POST'а после разрыва сети обязан вернуть тот же
-- ответ, а не создать второй объект. Храним и отпечаток запроса — тот же
-- ключ с другим телом это ошибка клиента, а не «ещё одна попытка».
CREATE TABLE partner_idempotency (
    partner_id   uuid NOT NULL REFERENCES partners(id) ON DELETE CASCADE,
    key          text NOT NULL,
    endpoint     text NOT NULL,
    request_hash text NOT NULL,
    status_code  integer NOT NULL,
    response     jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (partner_id, key)
);

CREATE INDEX partner_idempotency_created_ix ON partner_idempotency (created_at);

-- Поиск партнёра. B2C хранит разбор запроса в chat_searches, привязанных к
-- чату; у интеграции чата нет, поэтому поиск — самостоятельный ресурс со
-- своим id. Он же даёт постраничную выдачу и контекст для досье: без него
-- «насколько этот объект подходит запросу» не на что опереть.
CREATE TABLE partner_searches (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id     uuid NOT NULL REFERENCES partners(id) ON DELETE CASCADE,
    query          text NOT NULL,
    city           text NOT NULL,
    parsed_query   jsonb NOT NULL DEFAULT '{}',
    relaxed        text[] NOT NULL DEFAULT '{}',
    degraded       text[] NOT NULL DEFAULT '{}',
    notes          text[] NOT NULL DEFAULT '{}',
    data_freshness text NOT NULL DEFAULT '',
    area_label     text NOT NULL DEFAULT '',
    -- NULL — зоны не было; пустой объект значил бы «зона посчитана и пуста».
    area_geojson   jsonb,
    explanation    text NOT NULL DEFAULT '',
    total          integer NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX partner_searches_partner_ix ON partner_searches (partner_id, created_at DESC);

-- Результаты поиска партнёра. Форма повторяет chat_search_results: тот же
-- снимок ответа ML плюс лениво посчитанное досье с версией схемы и временем.
CREATE TABLE partner_search_results (
    search_id          uuid NOT NULL REFERENCES partner_searches(id) ON DELETE CASCADE,
    external_id        text NOT NULL,
    rank               integer NOT NULL,
    price              bigint,
    area               double precision,
    rooms              integer,
    address_facts      jsonb NOT NULL DEFAULT '{}',
    score              double precision NOT NULL DEFAULT 0,
    match_score        integer NOT NULL DEFAULT 0,
    dossier            jsonb,
    dossier_version    text,
    dossier_updated_at timestamptz,
    PRIMARY KEY (search_id, external_id)
);

CREATE INDEX partner_search_results_rank_ix ON partner_search_results (search_id, rank);

-- Вебхуки: заявка приходит в CRM партнёра сама. Без них единственный способ
-- узнать о заявке — опрашивать /leads, и между «покупатель написал» и
-- «продавец увидел» ложатся минуты опроса.
CREATE TABLE partner_webhooks (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id uuid NOT NULL REFERENCES partners(id) ON DELETE CASCADE,
    url        text NOT NULL,
    -- Секрет подписи. Хранится открыто намеренно: подпись HMAC вычисляется на
    -- нашей стороне при каждой доставке, а партнёру секрет нужно уметь
    -- показать повторно, иначе проверять подпись ему нечем.
    secret     text NOT NULL,
    events     text[] NOT NULL DEFAULT '{}',
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX partner_webhooks_partner_ix ON partner_webhooks (partner_id, created_at DESC);

-- Очередь доставок. Отдельная таблица, а не горутина «выстрелил и забыл»:
-- CRM партнёра лежит ровно в тот момент, когда пришла заявка, и повтор
-- должен пережить перезапуск шлюза.
CREATE TABLE partner_webhook_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id      uuid NOT NULL REFERENCES partner_webhooks(id) ON DELETE CASCADE,
    event           text NOT NULL,
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts        integer NOT NULL DEFAULT 0,
    last_error      text NOT NULL DEFAULT '',
    -- Момент следующей попытки: экспоненциальная выдержка живёт в строке, а не
    -- в памяти процесса.
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX partner_webhook_deliveries_due_ix
    ON partner_webhook_deliveries (next_attempt_at)
    WHERE status = 'pending';
