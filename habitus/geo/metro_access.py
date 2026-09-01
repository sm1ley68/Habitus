# habitus/geo/metro_access.py — пешие плечи «объект → платформа».
import logging

import psycopg
import requests

log = logging.getLogger(__name__)

WALK_SPEED_MPS = 1.33          # средняя пешая скорость
#: Коэффициент извилистости: прямая по воздуху систематически занижает время —
#: между домом и станцией бывает река, выемка путей или закрытый квартал.
WALK_DETOUR = 1.3


def straight_walk_seconds(metres: float) -> int:
    return int(round(metres * WALK_DETOUR / WALK_SPEED_MPS))


#: Потолок пешего плеча «объект/точка → платформа». Живёт здесь, в нижнем
#: слое, а не в habitus/online/metro_route.py, где появился первым (R63):
#: одним и тем же числом обязаны пользоваться ОБА писателя плеч — рантаймовый
#: `_nearest_stations_detailed` (произвольная точка запроса) и офлайновый
#: `refresh_listing_metro_access` ниже (строки доступа объявлений). Пока
#: потолок стоял только в рантайме (R92, сквозное ревью ветки), 112
#: московских объявлений имели ВСЕ строки доступа за потолком: SQL-фильтр по
#: времени на метро их пропускал (строки-то есть), а досье того же объекта
#: показывало пустое место вместо метро-блока — `door_to_door()` отбрасывал
#: те же платформы по потолку и честно деградировал до отсутствия. Два
#: разных ответа об одном объекте из одного числа — ровно то расхождение,
#: которого потолок в одном месте не может предотвратить по построению.
#:
#: Смысл числа не изменился (R69a): 3000 м geodesic — это ~49 минут ходьбы
#: по формуле выше, и это НЕ граница правдоподобия плеча, а ответ на вопрос
#: «есть ли тут вообще граф в зоне охвата» (соседний город, чужой регион,
#: ошибка геокодера).
MAX_ENTRY_WALK_METRES = 3000.0


class ORSWalker:
    """Пешая сеть через ORS. Вызывается как walker(start, end) → секунды."""

    def __init__(self, provider=None):
        # Импорт ВНУТРИ __init__, а не на уровне модуля. Сегодня цикла через
        # habitus.online.geo НЕТ (проверено: он не импортирует ничего из
        # habitus.geo) — прежняя формулировка комментария заявляла цикл,
        # которого не существует, и вводила в заблуждение. Импорт всё равно
        # остаётся отложенным по границе слоёв из R2: habitus/geo/* — нижний
        # слой, на который опирается habitus/online/*, а не наоборот, и
        # module-level импорт отсюда в habitus.online.geo эту границу
        # стирает. Task 9 кладёт код графа в habitus/online/metro_route.py,
        # который читает listing_metro_access и, скорее всего, потянет
        # habitus.geo.metro_access обратно — тогда цикл станет реальным, и
        # находить его через ImportError в проде дороже, чем держать этот
        # импорт отложенным уже сейчас.
        from habitus.online.geo import ORSProvider

        self._provider = provider or ORSProvider()

    def __call__(self, start: tuple[float, float],
                 end: tuple[float, float]) -> float | None:
        _, seconds = self._provider.directions(start, end, "foot-walking")
        return seconds


def _fetch_nearest_subway(cur, city: str, lon: float, lat: float):
    """Ближайшая ПОДЗЕМНАЯ платформа отдельным KNN, с той же поправкой на
    планарность, что и основной запрос ниже (буфер 5 вместо LIMIT 1 сразу)."""
    cur.execute("""
        SELECT s.id, ST_X(s.geom), ST_Y(s.geom),
               ST_Distance(s.geom::geography,
                           ST_SetSRID(ST_MakePoint(%(lon)s,%(lat)s),4326)::geography)
        FROM (SELECT st.id, st.geom
              FROM metro_station st JOIN metro_line ml ON ml.id = st.line_id
              WHERE st.city = %(city)s AND ml.system = 'subway'
              ORDER BY st.geom <-> ST_SetSRID(ST_MakePoint(%(lon)s,%(lat)s),4326)
              LIMIT 5) s
        ORDER BY 4
        LIMIT 1;""", {"city": city, "lon": lon, "lat": lat})
    return cur.fetchone()


def refresh_listing_metro_access(conn: psycopg.Connection, city: str,
                                 walker=None, k: int = 3,
                                 external_ids: list[str] | None = None) -> int:
    """Три ближайшие платформы на объект с пешим временем до каждой.

    Три, а не одна: ближайшая по прямой платформа регулярно оказывается на
    тупиковой ветке, тогда как вторая по близости стоит на пересадочном узле и
    даёт маршрут заметно короче. Выбор входа делает уже движок.

    Кандидаты добираются KNN-оператором <-> с запасом: он упорядочивает по
    ПЛАНАРНОМУ расстоянию в градусах, а планарно ближайшая точка на широте
    Москвы не всегда геодезически ближайшая (тот же приём и та же причина, что
    в habitus/geo/enrich.py).

    k ближайших берутся по ВСЕМ системам (subway/mck/mcd) — так и было
    задумано, Задаче 9 нужны МЦК/МЦД как входы графа. Но дом у диаметра без
    метро рядом может получить все k ближайших не-subway, и тогда
    walk_min_metro (проекция только на подземку, refresh_walk_min_metro ниже)
    останется NULL там, где прежний расчёт по прямой всегда давал значение.
    Поэтому ближайшая ПОДЗЕМНАЯ платформа гарантированно добирается отдельным
    запросом и добавляется к кандидатам, если её не оказалось среди k
    ближайших по всем системам — лишняя строка доступа у домов вдали от метро,
    но без нового способа потерять walk_min_metro.

    external_ids сужает обход до конкретных объявлений (публикация из
    кабинета, инкрементальный прогон) вместо всего города — на всём городе
    один SELECT+DELETE+k·INSERT на объявление стоит дорого, точечный вызов
    остаётся дешёвым.
    """
    written = 0
    with conn.cursor() as cur:
        cur.execute("""
            SELECT l.external_id, ST_X(l.geom), ST_Y(l.geom)
            FROM listings l
            WHERE l.city = %(city)s AND l.geom IS NOT NULL
              AND (%(ids)s::text[] IS NULL OR l.external_id = ANY(%(ids)s::text[]));""",
            {"city": city,
             "ids": list(external_ids) if external_ids is not None else None})
        listings = cur.fetchall()

        for ext_id, lon, lat in listings:
            cur.execute("""
                SELECT s.id, ST_X(s.geom), ST_Y(s.geom),
                       ST_Distance(s.geom::geography,
                                   ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography)
                FROM (SELECT id, geom FROM metro_station
                      WHERE city = %s
                      ORDER BY geom <-> ST_SetSRID(ST_MakePoint(%s,%s),4326)
                      LIMIT %s) s
                ORDER BY 4
                LIMIT %s;""",
                (lon, lat, city, lon, lat, max(k * 3, 9), k))
            # Забираем кандидатов ДО следующего execute на этом же курсоре:
            # DELETE ниже использует тот же cur и иначе стёр бы ещё не
            # прочитанный результат SELECT.
            candidates = cur.fetchall() or []

            guarantee = _fetch_nearest_subway(cur, city, lon, lat)
            if guarantee is not None and guarantee[0] not in {c[0] for c in candidates}:
                candidates.append(guarantee)

            # R92 (сквозное ревью ветки): тот же потолок, что у рантаймового
            # писателя плеч (см. MAX_ENTRY_WALK_METRES выше) — и на
            # гарантированную подземную платформу тоже. Гарантия защищает
            # walk_min_metro от NULL у дома, где ближайшие k платформ
            # оказались не-subway, но она не отменяет вопроса «есть ли тут
            # граф вообще»: объявление, у которого ВСЕ платформы дальше
            # потолка, теперь остаётся вовсе без строк доступа, а не с
            # оценкой в 50+ минут ходьбы, которую фильтр по времени принимал
            # за настоящее плечо. Цена решения измерена: 112 московских
            # объявлений (из 6738 со строками доступа) теряют строки доступа
            # целиком, и walk_min_metro у них становится NULL везде, где нет
            # walk_min_metro_src от источника — это отсутствие замера, а не
            # синтетический ноль, и ровно то же, что уже показывало досье.
            candidates = [c for c in candidates if c[3] <= MAX_ENTRY_WALK_METRES]

            cur.execute("DELETE FROM listing_metro_access WHERE external_id = %s;",
                        (ext_id,))
            for st_id, s_lon, s_lat, metres in candidates:
                seconds, estimated = straight_walk_seconds(metres), True
                if walker is not None:
                    try:
                        got = walker((lon, lat), (s_lon, s_lat))
                        if got is not None:
                            seconds, estimated = int(round(got)), False
                    except (requests.RequestException, KeyError, TypeError,
                            ValueError, RuntimeError, IndexError) as exc:
                        # Отказ пешего роутера деградирует ОДНУ станцию до
                        # оценки, а не роняет весь прогон: тем же принципом,
                        # которым защищён сбор POI в habitus/cli.py. IndexError
                        # — отдельно: ORSProvider.directions делает
                        # resp.json()["features"][0], и пустой ответ ORS
                        # бросает именно его, а не RequestException.
                        log.warning("пеший роутер отказал на %s→%s: %s",
                                    ext_id, st_id, exc)
                cur.execute("""
                    INSERT INTO listing_metro_access
                        (external_id, station_id, walk_seconds, estimated)
                    VALUES (%s,%s,%s,%s)
                    ON CONFLICT (external_id, station_id) DO UPDATE SET
                        walk_seconds = EXCLUDED.walk_seconds,
                        estimated = EXCLUDED.estimated, updated_at = now();""",
                    (ext_id, st_id, seconds, estimated))
                written += 1
    conn.commit()
    return written


def refresh_walk_min_metro(conn: psycopg.Connection, city: str,
                           external_ids: list[str] | None = None) -> int:
    """walk_min_metro из посчитанных плеч — вместо прямой по воздуху.

    ТОЛЬКО подземка: платформы МЦК и МЦД в это поле не подмешиваются (условие
    `ml.system = 'subway'` ниже — единственное место, где это применяется).
    Поле участвует в proximity-ранжировании и в SQL-фильтре
    `geo: [{kind: "metro"}]` (habitus/online/retrieval.py), а пороги гейта
    `eval --check` измерены на текущих данных
    (docs/notes/eval-baseline-2026-08-18.md) — тихая подмена смысла поля
    сдвинула бы выдачу и обесценила baseline.

    Обновляются ВСЕ объявления в скоупе (город × external_ids), а не только
    те, у кого нашлась строка в listing_metro_access: self-join с LEFT JOIN
    LATERAL ниже — иначе walk_min_metro_src (минуты от Циана) вообще не
    доезжает до listings в городе без единой subway-платформы (spb сегодня),
    а устаревшее вычисленное значение у объявления, чьи строки доступа
    пропали (снесённая станция, пересборка графа), молча переживает
    пересчёт вместо того, чтобы стать NULL.
    """
    with conn.cursor() as cur:
        cur.execute("""
            UPDATE listings l SET
                walk_min_metro = COALESCE(l.walk_min_metro_src, sub.minutes),
                metro_station = COALESCE(l.metro_station, sub.name),
                updated_at = now()
            FROM listings l2
            LEFT JOIN LATERAL (
                SELECT a.walk_seconds / 60.0 AS minutes, s.name
                FROM listing_metro_access a
                JOIN metro_station s ON s.id = a.station_id
                JOIN metro_line ml ON ml.id = s.line_id
                WHERE ml.system = 'subway' AND a.external_id = l2.external_id
                ORDER BY a.walk_seconds
                LIMIT 1
            ) sub ON true
            WHERE l.external_id = l2.external_id
              AND l2.city = %(city)s
              AND (%(ids)s::text[] IS NULL OR l2.external_id = ANY(%(ids)s::text[]));""",
            {"city": city,
             "ids": list(external_ids) if external_ids is not None else None})
        n = cur.rowcount
    conn.commit()
    return n


def refresh_metro_for_listings(conn: psycopg.Connection, city: str,
                               external_ids: list[str], walker=None,
                               k: int = 3) -> int:
    """Плечи + walk_min_metro для конкретных объявлений — один помощник на
    оба места, которым нужен точечный (не городской) пересчёт метро:
    публикация из личного кабинета (habitus/online/owner_listing.py) и
    инкрементальный прогон (habitus/update/incremental.py). До этой правки
    ни один из них не пересчитывал метро вовсе (Задача 7 отдала это только
    городскому refresh_listing_metro_access), и оба тихо переставали
    поддерживать walk_min_metro на своих путях (R39, R40).
    """
    written = refresh_listing_metro_access(conn, city, walker=walker, k=k,
                                           external_ids=external_ids)
    refresh_walk_min_metro(conn, city, external_ids=external_ids)
    return written
