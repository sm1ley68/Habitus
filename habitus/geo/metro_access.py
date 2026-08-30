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


class ORSWalker:
    """Пешая сеть через ORS. Вызывается как walker(start, end) → секунды."""

    def __init__(self, provider=None):
        # Импорт ВНУТРИ функции/метода, а не на уровне модуля (R2): ORSProvider
        # живёт в habitus.online.geo, а тот модуль (через цепочку импортов)
        # тянет обратно в habitus.geo — импорт на уровне модуля здесь дал бы
        # цикл, как и straight_walk_seconds → habitus.online.geo в Задачах 9/11.
        from habitus.online.geo import ORSProvider

        self._provider = provider or ORSProvider()

    def __call__(self, start: tuple[float, float],
                 end: tuple[float, float]) -> float | None:
        _, seconds = self._provider.directions(start, end, "foot-walking")
        return seconds


def refresh_listing_metro_access(conn: psycopg.Connection, city: str,
                                 walker=None, k: int = 3) -> int:
    """Три ближайшие платформы на объект с пешим временем до каждой.

    Три, а не одна: ближайшая по прямой платформа регулярно оказывается на
    тупиковой ветке, тогда как вторая по близости стоит на пересадочном узле и
    даёт маршрут заметно короче. Выбор входа делает уже движок.

    Кандидаты добираются KNN-оператором <-> с запасом: он упорядочивает по
    ПЛАНАРНОМУ расстоянию в градусах, а планарно ближайшая точка на широте
    Москвы не всегда геодезически ближайшая (тот же приём и та же причина, что
    в habitus/geo/enrich.py).
    """
    written = 0
    with conn.cursor() as cur:
        cur.execute("""
            SELECT l.external_id, ST_X(l.geom), ST_Y(l.geom)
            FROM listings l
            WHERE l.city = %s AND l.geom IS NOT NULL;""", (city,))
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
                            ValueError, RuntimeError) as exc:
                        # Отказ пешего роутера деградирует ОДНУ станцию до
                        # оценки, а не роняет весь прогон: тем же принципом,
                        # которым защищён сбор POI в habitus/cli.py.
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


def refresh_walk_min_metro(conn: psycopg.Connection, city: str) -> int:
    """walk_min_metro из посчитанных плеч — вместо прямой по воздуху.

    ТОЛЬКО подземка: платформы МЦК и МЦД в это поле не подмешиваются (условие
    `ml.system = 'subway'` ниже — единственное место, где это применяется).
    Поле участвует в proximity-ранжировании и в SQL-фильтре
    `geo: [{kind: "metro"}]` (habitus/online/retrieval.py), а пороги гейта
    `eval --check` измерены на текущих данных
    (docs/notes/eval-baseline-2026-08-18.md) — тихая подмена смысла поля
    сдвинула бы выдачу и обесценила baseline.
    """
    with conn.cursor() as cur:
        cur.execute("""
            UPDATE listings l SET
                walk_min_metro = COALESCE(l.walk_min_metro_src, sub.minutes),
                metro_station = COALESCE(l.metro_station, sub.name),
                updated_at = now()
            FROM (
                SELECT DISTINCT ON (a.external_id)
                       a.external_id, a.walk_seconds / 60.0 AS minutes, s.name
                FROM listing_metro_access a
                JOIN metro_station s ON s.id = a.station_id
                JOIN metro_line ml ON ml.id = s.line_id
                WHERE ml.system = 'subway' AND s.city = %s
                ORDER BY a.external_id, a.walk_seconds
            ) sub
            WHERE l.external_id = sub.external_id AND l.city = %s;""",
            (city, city))
        n = cur.rowcount
    conn.commit()
    return n
