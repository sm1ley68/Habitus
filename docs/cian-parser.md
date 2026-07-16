# Парсер Cian

Команда `backend/cmd/cian-parser` собирает объявления о продаже квартир через
внутренний endpoint Cian `search-offers-desktop`. В выгрузку попадают проза
`description`, цена, площадь, комнаты, этаж, адрес, метро, ЖК, материал дома,
координаты и URL.

## Что нужно до запуска

Cian часто возвращает HTML-капчу с HTTP 200 для дата-центровых и домашних IP.
Для рабочего сбора нужен хотя бы один резидентный или мобильный HTTP/SOCKS5
прокси. Парсер не покупает и не настраивает прокси автоматически.

Перед использованием проверьте, что сбор разрешён условиями источника и
применимыми правилами обработки данных. Не коммитьте прокси-логины: `.env`,
`data/` и `.cian-proxies` исключены из Git.

Прокси можно передать тремя способами:

```bash
export CIAN_PROXIES='http://user:password@host-1:port,http://user:password@host-2:port'
```

```bash
cd backend
go run ./cmd/cian-parser \
  --proxy 'http://user:password@host-1:port' \
  --proxy 'socks5://user:password@host-2:port'
```

Или положить по одному URL на строку в `.cian-proxies` и передать
`--proxy-file ../.cian-proxies`. Пустые строки и строки с `#` игнорируются.

## Запуск

Базовый сбор Москвы до 5000 уникальных объявлений:

```bash
cd backend
go run ./cmd/cian-parser \
  --output ../data/cian/listings.csv \
  --rooms 1,2,3,4 \
  --pages 54 \
  --max-offers 5000
```

Если одно окно выдачи упирается в лимит Cian, его можно раздробить по цене:

```bash
cd backend
go run ./cmd/cian-parser \
  --output ../data/cian/listings.csv \
  --rooms 1,2,3,4 \
  --price-ranges '0:15000000,15000001:30000000,30000001:0' \
  --pages 54 \
  --max-offers 5000
```

Для JSON достаточно расширения файла либо явного формата:

```bash
cd backend
go run ./cmd/cian-parser --output ../data/cian/listings.json --format json
```

Прямое подключение по умолчанию запрещено, потому что почти всегда даёт
капчу. Для разовой диагностики без прокси есть `--allow-direct`.

## Поведение

- Для каждой прокси создаётся отдельный cookie-jar и выполняется начальный
  заход на `https://www.cian.ru/`.
- TLS и HTTP/2 fingerprint согласованы с одним из поддерживаемых Chrome
  профилей; User-Agent и `sec-ch-ua` меняются вместе с профилем.
- Сессии чередуются между запросами. При HTML/captcha, HTTP 403/429 или сетевой
  ошибке заблокированная сессия закрывается, создаётся заново и запрос
  повторяется через другую сессию.
- Между запросами выдерживается случайная задержка 3–6 секунд. Значения можно
  изменить через `--delay-min` и `--delay-max`.
- Фильтры комнатности и цены обходятся по страницам round-robin, поэтому
  `--max-offers` не заполняет весь датасет первым фильтром.
- После каждой страницы результат сохраняется атомарно. Повторный запуск читает
  существующий файл и обновляет строки по стабильному `cian_id`, поэтому
  оборванный сбор можно безопасно запустить снова.
- Офферы без стабильного id или без `description` пропускаются.
- URL и credentials прокси не выводятся в лог.

CSV содержит колонки:

```text
cian_id,description,price,area,rooms,floor,floors,address,metro,zhk,
building_material,deadline,latitude,longitude,url,collected_at
```

`metro` хранится в CSV как JSON-массив объектов `name`, `time`,
`transport_type`. В JSON-выгрузке это обычный вложенный массив.

Полный список флагов:

```bash
cd backend
go run ./cmd/cian-parser --help
```
