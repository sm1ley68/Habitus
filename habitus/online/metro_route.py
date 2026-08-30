# habitus/online/metro_route.py — граф рельсового транспорта в памяти и Дейкстра.
#
# Граф крошечный (порядка 300 узлов в Москве, 70 в Петербурге), поэтому обход
# считается за доли миллисекунды и предрасчитанная матрица не нужна: и число
# для SQL-фильтра (Задача 12), и разбивка маршрута для отрисовки (Задача 11)
# берутся из ОДНОГО обхода одного графа _dijkstra(). Это НЕ гарантирует само
# по себе, что times_from() и route() дают одно и то же число для одной и
# той же станции — семя, которое одновременно и цель, нужно ещё явно свести
# к общей формуле min(пеший заход, рельсовый путь) в обоих местах (R56,
# фикс-раунд 1) — иначе они расходятся именно в этой точке.
import heapq
from dataclasses import dataclass, field

import psycopg


@dataclass(frozen=True)
class Station:
    id: int
    name: str
    line_ref: str
    line_name: str
    system: str
    colour: str | None
    lon: float
    lat: float


@dataclass
class Segment:
    line_ref: str
    line_name: str
    system: str
    colour: str | None
    from_station: str
    to_station: str
    stops: int
    seconds: int
    estimated: bool = False


@dataclass
class Transfer:
    from_station: str
    to_station: str
    seconds: int
    estimated: bool = False
    outdoor: bool = False


@dataclass
class MetroRoute:
    segments: list[Segment] = field(default_factory=list)
    transfers: list[Transfer] = field(default_factory=list)
    ride_seconds: int = 0
    estimated: bool = False


@dataclass
class MetroGraph:
    stations: dict[int, Station]
    #: (from_id, to_id) → (секунды, оценка ли)
    edges: dict[tuple[int, int], tuple[int, bool]]
    #: (from_id, to_id) → (секунды, оценка ли, улицей ли)
    transfers: dict[tuple[int, int], tuple[int, bool, bool]]
    #: (system, ref) → (интервал в секундах, оценка ли). Ключ ОБЯЗАН включать
    #: system: metro_line уникален по (city, system, ref), и одинаковый ref в
    #: разных системах (сегодня с реальными данными не встречается — '14'
    #: только у МЦК, 'D*' только у МЦД, но это свойство данных, а не гарантия
    #: схемы) не должен схлопывать два разных интервала в один.
    headways: dict[tuple[str, str], tuple[int, bool]]
    #: Кэш смежности на процесс. Обычное поле датакласса (R3) — MetroGraph не
    #: frozen, никаких трюков с object.__setattr__ не нужно.
    _adj: dict[int, list[tuple[int, int, str]]] | None = field(
        default=None, init=False, repr=False)

    def _headway(self, station: Station) -> tuple[int, bool]:
        """(секунды интервала линии станции, оценка ли).

        R29/R30: нет дефолта 0. Отсутствующая запись — баг построения графа
        (Задача 6 не заполнила headway_s для какой-то линии), а не «интервала
        нет»; подстановка нуля показала бы пользователю «ждать не придётся»
        как измеренный факт — синтетический ноль вместо отсутствующего
        замера запрещён проектом (CLAUDE.md). Поэтому здесь KeyError, а не
        .get(key, 0).
        """
        key = (station.system, station.line_ref)
        if key not in self.headways:
            raise KeyError(
                f"нет headway для линии {key} (станция id={station.id} "
                f"{station.name!r}) — граф построен не полностью")
        return self.headways[key]

    def _adjacency(self) -> dict[int, list[tuple[int, int, str]]]:
        """id → [(сосед, секунды ребра с учётом интервала, вид перехода)].

        Строится лениво один раз. Интервал линии, на которую садимся при
        пересадке, зашивается в вес ребра пересадки здесь же — ждать поезд
        придётся в любом случае, это НЕ отдельная сущность графа.
        """
        if self._adj is None:
            adj: dict[int, list[tuple[int, int, str]]] = {}
            for (a, b), (sec, _est) in self.edges.items():
                adj.setdefault(a, []).append((b, sec, "ride"))
            for (a, b), (sec, _est, _outdoor) in self.transfers.items():
                # Вес ребра пересадки включает интервал линии станции b
                # ЦЕЛИКОМ, даже если b и есть конечная цель маршрута (перешёл
                # на платформу другой линии — это и есть цель, садиться на
                # неё уже не нужно). Модель платит headway в любом случае:
                # это консервативно (никогда не занижает число, только
                # изредка завышает) на редком классе "цель — соседняя
                # пересадочная платформа". Осознанно оставлено комментарием,
                # а не фиксом (фикс-раунд 1, минор R58).
                headway_sec, _headway_est = self._headway(self.stations[b])
                adj.setdefault(a, []).append((b, sec + headway_sec, "transfer"))
            self._adj = adj
        return self._adj

    def _dijkstra(self, seeds: dict[int, int]
                  ) -> tuple[dict[int, int], dict[int, tuple[int, str]]]:
        """seeds — «станция → уже потраченные секунды» (пешее плечо до входа).

        Несколько источников за один обход: у точки ближайших платформ
        несколько, и каждая входит в очередь со своим плечом. Минимум по
        вариантам входа получается сам собой, а не отдельным сравнением
        снаружи.

        ИЗВЕСТНОЕ ОГРАНИЧЕНИЕ МОДЕЛИ (R6): обход всегда стартует от seeds — в
        SQL-фильтре (Задача 12) это станции у места НАЗНАЧЕНИЯ (один обход
        переиспользуется для всех объявлений сразу), в route() это тоже
        сторона, которую передал вызывающий. Интервал, добавляемый ниже при
        входе на граф, — это интервал линии станции ПОСЕВА, а не линии, на
        которую пассажир садится рядом с домом. Граф неориентированный, и
        время в пути по рёбрам от направления обхода не зависит — но headway
        зависит. Для пары метро↔метро это несущественно (интервалы близки
        друг к другу), а вот на паре метро↔МЦД расхождение доходит примерно
        до ±8 минут (метро ~2 мин, МЦД днём до ~12 мин на редком классе
        «дом у метро → работа у МЦД»). Это принятое допущение статической
        модели графа, а не забытый баг — направленный обход намеренно не
        делается (см. бриф Задачи 9 и controller ruling R6).
        """
        adj = self._adjacency()
        dist: dict[int, int] = {}
        prev: dict[int, tuple[int, str]] = {}
        heap: list[tuple[int, int]] = []
        for sid, walk in seeds.items():
            if sid not in self.stations:
                continue
            # посадка на линию станции посева тоже стоит интервала (см.
            # докстроку выше — вот то самое место, где это происходит)
            headway_sec, _headway_est = self._headway(self.stations[sid])
            start = walk + headway_sec
            if sid not in dist or start < dist[sid]:
                dist[sid] = start
                heapq.heappush(heap, (start, sid))
        while heap:
            d, node = heapq.heappop(heap)
            if d > dist.get(node, d):
                continue
            for nxt, sec, kind in adj.get(node, ()):
                nd = d + sec
                if nd < dist.get(nxt, nd + 1):
                    dist[nxt] = nd
                    prev[nxt] = (node, kind)
                    heapq.heappush(heap, (nd, nxt))
        return dist, prev

    def times_from(self, seeds: dict[int, int]) -> dict[int, int]:
        """Секунды до каждой достижимой платформы. Один обход one-to-all.

        R56 (фикс-раунд 1): для станций-семян результат — min(голый пеший
        `walk`, рельсовый путь через граф), а НЕ безусловный `walk`. До
        фикса здесь стояла безусловная подмена на `walk` — она давала
        правильный 0, когда seed совпадает с целью, но когда один seed
        достижим ДЕШЕВЛЕ рельсами от другого seed (например, семена
        {1: 0, 3: 900}, где до станции 3 рельсами от станции 1 всего 320 с),
        отбрасывала более короткий рельсовый путь и завышала число —
        SQL-фильтр (Задача 12) занижал бы доступность и терял подходящие
        объявления.

        `route()` использует ТОТ ЖЕ min() на тех же величинах (см. его
        докстроку) — этим гарантируется, что оба метода не могут разойтись
        в числах для одной и той же станции: `route(S, {t: w}).ride_seconds
        == times_from(S)[t] + w` всегда, когда t достижима.
        """
        dist, _ = self._dijkstra(seeds)
        for sid, walk in seeds.items():
            if sid in dist:
                dist[sid] = min(walk, dist[sid])
        return dist

    def route(self, seeds: dict[int, int],
              targets: dict[int, int]) -> MetroRoute | None:
        """Лучший маршрут между наборами входов и выходов, с разбивкой на
        отрезки одной линии (Segment) и пересадки (Transfer).

        R56 (фикс-раунд 1): у каждой целевой станции — до двух кандидатов на
        итоговое время: "тривиальный" (её id есть среди seeds — цель
        совпадает со входом, ехать не нужно вовсе) и "рельсовый" (обычный
        путь по графу). Побеждает меньший — та же формула минимума, что и в
        `times_from()`, поэтому оба метода НЕ МОГУТ разойтись в числах для
        одной и той же станции (см. докстроку `times_from` и таблицу сверки
        в отчёте задачи). Раньше здесь всегда строился рельсовый путь, и для
        seed == target это давало фантомную поездку (headway + 0 рёбер) —
        ожидание поезда, на который пассажир никогда не садится.

        R54 (фикс-раунд 1): `MetroRoute.estimated` намеренно НЕ учитывает
        пешие плечи входа/выхода — `seeds`/`targets` это голые секунды без
        признака оценки, и graph о них ничего не знает по зафиксированному
        контракту интерфейса. `estimated` здесь — только про рельсовую
        часть: рёбра, пересадки, интервалы. Итоговую честность (рельсы ИЛИ
        пешие плечи) обязан досчитать вызывающий — Задача 11, у которой
        есть `listing_metro_access.estimated` для обоих плеч; забыть OR-нуть
        его там — значит показать оценочное пешее плечо как измеренный
        факт, что запрещено проектом.
        """
        if not seeds or not targets:
            return None
        dist, prev = self._dijkstra(seeds)

        best: tuple[int, str, int] | None = None   # (total, kind, target_id)
        for t, walk in targets.items():
            candidates: list[tuple[int, str]] = []
            if t in seeds:
                candidates.append((seeds[t] + walk, "trivial"))
            if t in dist:
                candidates.append((dist[t] + walk, "rail"))
            if not candidates:
                continue
            total_t, kind_t = min(candidates)
            if best is None or total_t < best[0]:
                best = (total_t, kind_t, t)
        if best is None:
            return None
        total, kind, end = best

        if kind == "trivial":
            # Цель совпадает со входом, и рельсовый путь до неё не дешевле
            # прямого пешего захода — ехать незачем. Ни headway, ни
            # сегментов: "прокат" через граф здесь был бы неоплаченной
            # ложью пользователю (R56, случай (a) из фикс-раунда 1).
            return MetroRoute(ride_seconds=total)

        path: list[int] = [end]
        kinds: list[str] = []
        while path[-1] in prev:
            node, edge_kind = prev[path[-1]]
            kinds.append(edge_kind)
            path.append(node)
        path.reverse()
        kinds.reverse()

        route = MetroRoute(ride_seconds=total)
        # Интервал линии станции посева тоже часть честности маршрута — тот
        # же учёт, что и в _dijkstra, но здесь он должен попасть в
        # route.estimated (R29/R30: флаг — OR всех оценочных вводов).
        _, seed_headway_est = self._headway(self.stations[path[0]])
        route.estimated = seed_headway_est

        run_start = 0   # индекс в path, с которого начинается текущий отрезок
        for i, edge_kind in enumerate(kinds):
            a, b = path[i], path[i + 1]
            if edge_kind == "ride":
                if self.edges[(a, b)][1]:
                    route.estimated = True
                continue
            # Пересадка закрывает текущий отрезок path[run_start : i+1].
            if i > run_start:
                route.segments.append(self._segment(path[run_start:i + 1]))
            sec, est, outdoor = self.transfers[(a, b)]
            route.transfers.append(Transfer(
                from_station=self.stations[a].name,
                to_station=self.stations[b].name,
                seconds=sec, estimated=est, outdoor=outdoor))
            # Интервал линии, на которую садимся после пересадки, — тоже
            # оценочный ввод, красящий маршрут, даже если сама Transfer не
            # estimated (она хранит только честность пешей части перехода).
            _, headway_est = self._headway(self.stations[b])
            route.estimated = route.estimated or est or headway_est
            run_start = i + 1
        if len(path) - 1 > run_start:
            route.segments.append(self._segment(path[run_start:]))
        return route

    def _segment(self, path_slice: list[int]) -> Segment:
        """Один отрезок пути на одной линии — сумма рёбер вдоль ФАКТИЧЕСКИ
        пройденного среза path (R4).

        Не ищет «следующую станцию с тем же line_ref»: на кольцевой линии у
        станции всегда два соседа с одинаковым line_ref (по и против часовой
        стрелки), и такой поиск выбрал бы направление произвольно — вплоть до
        противоположного тому, что реально нашла Дейкстра. Срез уже
        пройденного пути этой неоднозначности не имеет по построению.
        """
        st = self.stations[path_slice[0]]
        seconds, estimated = 0, False
        for i in range(len(path_slice) - 1):
            sec, est = self.edges[(path_slice[i], path_slice[i + 1])]
            seconds += sec
            estimated = estimated or est
        return Segment(
            line_ref=st.line_ref, line_name=st.line_name, system=st.system,
            colour=st.colour, from_station=st.name,
            to_station=self.stations[path_slice[-1]].name,
            stops=len(path_slice) - 1, seconds=seconds, estimated=estimated)


#: Кэш графа на процесс: ключ — (город, отпечаток свежести графа).
_GRAPH_CACHE: dict[tuple[str, str], MetroGraph] = {}


def clear_graph_cache() -> None:
    _GRAPH_CACHE.clear()


def _fingerprint(conn: psycopg.Connection, city: str) -> str:
    row = conn.execute("""
        SELECT COALESCE(max(updated_at)::text,'-') || ':' || count(*)
        FROM metro_station WHERE city = %s;""", (city,)).fetchone()
    return row[0] if row else "-"


def load_graph(conn: psycopg.Connection, city: str) -> MetroGraph | None:
    """Граф города из БД с кэшем на процесс. None — графа для города нет.

    Инвалидация по отпечатку (max updated_at + число станций): пересборка
    графа меняет его, и следующий запрос перечитает таблицы сам.
    """
    key = (city, _fingerprint(conn, city))
    if key in _GRAPH_CACHE:
        return _GRAPH_CACHE[key]

    rows = conn.execute("""
        SELECT s.id, s.name, ml.ref, ml.name, ml.system, ml.colour,
               ST_X(s.geom), ST_Y(s.geom), ml.headway_s, ml.headway_estimated
        FROM metro_station s JOIN metro_line ml ON ml.id = s.line_id
        WHERE s.city = %s;""", (city,)).fetchall()
    if not rows:
        return None

    stations: dict[int, Station] = {}
    headways: dict[tuple[str, str], tuple[int, bool]] = {}
    for (sid, name, ref, lname, system, colour, lon, lat, headway_s,
         headway_est) in rows:
        stations[sid] = Station(sid, name, ref, lname, system, colour, lon, lat)
        headways[(system, ref)] = (headway_s, headway_est)

    edges = {(a, b): (sec, est) for a, b, sec, est in conn.execute(
        "SELECT from_station, to_station, seconds, estimated FROM metro_edge "
        "WHERE city = %s;", (city,)).fetchall()}
    transfers = {(a, b): (sec, est, out) for a, b, sec, est, out in conn.execute(
        "SELECT from_station, to_station, seconds, estimated, outdoor "
        "FROM metro_transfer WHERE city = %s;", (city,)).fetchall()}

    graph = MetroGraph(stations=stations, edges=edges, transfers=transfers,
                       headways=headways)
    _GRAPH_CACHE.clear()          # держим только актуальный отпечаток
    _GRAPH_CACHE[key] = graph
    return graph
