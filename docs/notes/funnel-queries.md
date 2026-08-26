# Воронка MVP: как её читать

Журнал — `product_events` (Go-миграция `0015`). События пишутся в хендлерах:
`guest_created`, `guest_upgraded`, `search_started`, `passport_opened`,
`favorite_added`, `feedback_given`, `lead_sent`.

Чего в журнале НЕТ намеренно: завершённые поиски и их выдача уже лежат в
`chat_searches` / `chat_search_results`, а текст запроса — в `messages` и
`chat_searches.raw_query`. Дублировать это событием значило бы завести второй
источник правды об одном факте.

Подключение: `psql postgresql://habitus:habitus@localhost:5544/habitus`.

## Воронка за период, по людям

```sql
SELECT
    count(DISTINCT user_id) FILTER (WHERE kind = 'search_started')  AS искали,
    count(DISTINCT user_id) FILTER (WHERE kind = 'passport_opened') AS открыли_паспорт,
    count(DISTINCT user_id) FILTER (WHERE kind = 'favorite_added')  AS сохранили,
    count(DISTINCT user_id) FILTER (WHERE kind = 'lead_sent')       AS отправили_заявку
FROM product_events
WHERE created_at >= now() - interval '30 days';
```

## Гость против зарегистрированного

Проверяет главную ставку гостевого входа: доходит ли аноним до ценности.

```sql
SELECT is_guest,
       count(DISTINCT user_id) FILTER (WHERE kind = 'search_started')  AS искали,
       count(DISTINCT user_id) FILTER (WHERE kind = 'passport_opened') AS открыли_паспорт
FROM product_events
WHERE created_at >= now() - interval '30 days'
GROUP BY is_guest;
```

## Конверсия гостя в аккаунт

```sql
SELECT
    count(*) FILTER (WHERE kind = 'guest_created')  AS гостей,
    count(*) FILTER (WHERE kind = 'guest_upgraded') AS зарегистрировались
FROM product_events
WHERE created_at >= now() - interval '30 days';
```

Откуда пришла регистрация. `lead_form` — человек завёл аккаунт прямо в форме
заявки: это самая ценная половина, и держать её в одной куче с обычной
регистрацией значит не увидеть, работает ли эта точка входа.

```sql
SELECT COALESCE(props->>'source', 'auth_form') AS откуда, count(*) AS сколько
FROM product_events
WHERE kind = 'guest_upgraded' AND created_at >= now() - interval '30 days'
GROUP BY 1 ORDER BY 2 DESC;
```

## Было ли куда отправлять заявку

Если конверсия в заявку низкая, сначала смотреть сюда: возможно, у открытых
объектов просто не было продавца в системе.

```sql
SELECT props->>'contact_kind' AS способ_связи, count(*) AS открытий
FROM product_events
WHERE kind = 'passport_opened' AND created_at >= now() - interval '30 days'
GROUP BY 1 ORDER BY 2 DESC;
```

## Качество подбора глазами пользователей

```sql
SELECT verdict, count(*) AS оценок,
       count(*) FILTER (WHERE reason <> '') AS с_объяснением
FROM result_feedback
WHERE created_at >= now() - interval '30 days'
GROUP BY verdict;
```

Причины отказов — что чинить в первую очередь:

```sql
SELECT reason, count(*) AS сколько_раз
FROM result_feedback
WHERE verdict = 'down' AND reason <> ''
  AND created_at >= now() - interval '30 days'
GROUP BY reason ORDER BY 2 DESC LIMIT 20;
```

## Доля пустых выдач

Из `chat_search_results`, не из журнала: поиски там и так все.

```sql
SELECT count(*) AS поисков,
       count(*) FILTER (WHERE r.n = 0) AS пустых
FROM chat_searches cs
LEFT JOIN LATERAL (
    SELECT count(*) AS n FROM chat_search_results csr WHERE csr.search_id = cs.id
) r ON true
WHERE cs.created_at >= now() - interval '30 days';
```
