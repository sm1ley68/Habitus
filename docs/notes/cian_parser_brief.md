# Бриф: парсер Cian.ru (для реализации на Go)

## Цель
Достать объявления о продаже квартир в Москве с **полем `description` (свободный текст-проза)**
и структурными полями (цена, площадь, комнаты, этаж, адрес, метро, ЖК). Данные нужны
для ML-пайплайна (эмбеддинги BGE-M3 + гибридный поиск). Нужен объём ~1–5k карточек, дозагрузка инкрементально.

## Что уже проверено (Python) и почему НЕ работает
Проблема **не в коде парсинга — код тривиален.** Проблема в **anti-bot Циана на уровне IP/сети.**

Проверены три подхода, все три упираются в одну стену:

1. **HTML-скрейп** списка `https://www.cian.ru/cat.php?...` → отдаётся страница captcha, 0 карточек.
2. **cianparser** (Python-либа на CloudScraper, обход Cloudflare) → уходит в бесконечный ретрай
   стартовой страницы (1199 повторов за ~6 мин), 0 карточек. CloudScraper НЕ пробивает.
3. **Внутренний JSON-API** `POST https://api.cian.ru/search-offers/v2/search-offers-desktop/`
   → `HTTP 200`, но `Content-Type: text/html` и тело = `<title>Captcha - база объявлений ЦИАН</title>`.

Вывод: с дата-центрового/домашнего IP Циан **до отдачи данных** заворачивает запрос на captcha-заглушку.
Отдаёт 200 (не 403!) с HTML-капчей вместо JSON. Значит любой «правильный парсер» без смены сетевого
профиля даст тот же результат.

## Целевой endpoint (это то, что надо бить — тут есть `description`)

```
POST https://api.cian.ru/search-offers/v2/search-offers-desktop/
Content-Type: application/json
```

Тело запроса (регион 1 = Москва, комнатность=1, страница 1):
```json
{
  "jsonQuery": {
    "region":         {"type": "terms", "value": [1]},
    "_type":          "flatsale",
    "engine_version": {"type": "term",  "value": 2},
    "room":           {"type": "terms", "value": [1]},
    "page":           {"type": "term",  "value": 1}
  }
}
```
- Пагинация: инкремент `page.value` (на странице ~28 офферов; лимит выдачи Циана ~54 страницы/фильтр,
  поэтому дробить по фильтрам: комнатность 1/2/3/4, диапазоны цены и т.д. — обходить лимит в 5000 выдачи).
- Ответ (когда НЕ заблокирован): JSON, офферы в `data.offersSerialized[]`. У каждого оффера:
  - `description` — **проза-описание (то, что нам нужно)**
  - `geo.userInput` / `geo.address[]` — адрес; `geo.undergrounds[]` — метро (name + time + transportType)
  - `bargainTerms.price` — цена; `totalArea`, `roomsCount`, `floorNumber`, `building.floorsCount`
  - `building.materialType`, `building.deadline` (новостройки), `newbuilding.name` — название ЖК
  - `cianId` / `id` — стабильный идентификатор оффера (для дедупа/инкремента)

## В чём именно техническая проблема (что решать в Go)

Anti-bot Циана (собственный + элементы Qrator) фингерпринтит клиента по нескольким осям.
Чтобы пройти, надо закрыть ВСЕ:

1. **Репутация IP (главное).** Дата-центровые и часто домашние IP → сразу captcha.
   Решение: **резидентные/мобильные прокси** с ротацией. Без этого остальное бессмысленно.

2. **TLS-фингерпринт (JA3/JA4).** Дефолтный TLS-стек Go (`crypto/tls`) имеет ClientHello,
   отличный от Chrome, — anti-bot это палит. Решение: **utls**
   (`github.com/refraction-networking/utls`) с профилем `HelloChrome_120`/актуальным,
   чтобы ClientHello совпадал с настоящим Chrome. Это критично для Go (в Python с cloudscraper
   проблема та же, поэтому он и не пробил).

3. **Cookie/JS-challenge.** Первый заход должен собрать куки с `https://www.cian.ru/`
   (напр. `_CIAN_GK`, `session_region_id`, куки anti-bot-челленджа), затем переиспользовать
   их в запросах к `api.cian.ru`. Возможен JS-челлендж — тогда нужен headless (rod/chromedp)
   для первичной инициализации сессии, дальше — обычные HTTP-запросы с полученными куками.

4. **Заголовки как у браузера.** Полный набор: реальный `User-Agent` Chrome,
   `Origin: https://www.cian.ru`, `Referer: https://www.cian.ru/...`,
   `Accept`, `Accept-Language: ru-RU,ru;q=0.9`, `sec-ch-ua*`, `sec-fetch-*`.

5. **Поведение/темп.** Задержки 3–6 с между запросами, ротация UA+прокси, ретрай с бэкоффом.
   Детектить блок по признаку: тело содержит `Captcha - база объявлений ЦИАН` или
   `Content-Type: text/html` на API-эндпоинте → сменить прокси и повторить.

## Рекомендуемый стек на Go
- HTTP+TLS-маскировка: `github.com/refraction-networking/utls` (+ `net/http` поверх utls-конна,
  или `github.com/bogdanfinn/tls-client` — обёртка, где JA3/HTTP2-фингерпринт уже настроены под браузеры).
- Прокси: резидентные/мобильные, с ротацией per-request.
- (Опционально) headless для инициализации куки-сессии: `github.com/go-rod/rod`.
- Парсинг: стандартный `encoding/json` по структуре `offersSerialized`.

## Минимальный план проверки (в таком порядке)
1. Взять 1 резидентный прокси. Обычным `net/http` дёрнуть JSON-API — уже может хватить.
2. Если всё ещё captcha → добавить **utls** (Chrome ClientHello). Это обычно решает.
3. Если и это не всё → headless-инициализация куки на `www.cian.ru`, потом API с куками.
4. Как только пошёл JSON — распарсить `data.offersSerialized[]`, вынуть `description` и структуру,
   писать в CSV/JSON: `cian_id, description, price, area, rooms, floor, floors, address, metro[], zhk`.

## Критерий успеха
Ответ API = `Content-Type: application/json`, `data.offersSerialized` не пустой,
у офферов заполнено `description`. Тогда отдать CSV мне — прогоню через оффлайн-пайплайн.
