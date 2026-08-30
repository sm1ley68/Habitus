import sys

import psycopg
from pathlib import Path
from habitus.config import settings
from habitus.cli import run_offline, build_metro
from habitus.db.init_db import init_db
from habitus.geo.metro import LineRaw, StationRaw
from habitus.geo.osm_extract import POI_KINDS

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"


class FakeModel:
    def encode(self, texts, **kw):
        return {"dense_vecs": [[0.1] * settings.embed_dim for _ in texts],
                "lexical_weights": [{"5": 0.5} for _ in texts]}


def _no_network_geocoder(addr, session=None):
    raise AssertionError("geocoder не должен вызываться в smoke-тесте (нет сети)")


def test_run_offline_end_to_end():
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        stats = run_offline(FIX, conn, model=FakeModel(), fetch_osm=False,
                            geocoder=_no_network_geocoder)
        assert stats["raw"] == 2
        assert stats["listings"] == 2
        assert stats["embedded"] == 2
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings "
                        "WHERE embedding IS NOT NULL AND doc_text IS NOT NULL;")
            assert cur.fetchone()[0] == 2


CIAN_FIX = Path(__file__).parent / "fixtures" / "sample_cian.csv"


def test_run_offline_deactivates_listings_gone_from_source():
    """Повторный прогон с усечённым снимком обязан гасить пропавшее объявление —
    иначе база копит снятые с продажи квартиры и они остаются в выдаче."""
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        run_offline(CIAN_FIX, conn, model=FakeModel(), fetch_osm=False,
                    geocoder=_no_network_geocoder, source="cian")
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE is_active;")
            before = cur.fetchone()[0]
        assert before == 2

        # снимок, где осталось только первое объявление
        rows = CIAN_FIX.read_text(encoding="utf-8").splitlines()
        shrunk = Path(conn.info.dbname + "_shrunk.csv")
        shrunk.write_text("\n".join(rows[:2]) + "\n", encoding="utf-8")
        try:
            stats = run_offline(shrunk, conn, model=FakeModel(), fetch_osm=False,
                                geocoder=_no_network_geocoder, source="cian")
        finally:
            shrunk.unlink(missing_ok=True)
        assert stats["deactivated"] == 1
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE is_active;")
            assert cur.fetchone()[0] == 1


def test_osm_failure_does_not_abort_the_cycle(monkeypatch):
    """Overpass отваливается регулярно (504 и таймауты положили 4 цикла подряд).
    Точки города меняются раз в месяцы, а объявления — каждый час: сбой чужого
    API не имеет права уносить с собой заливку, обогащение и эмбеддинги."""
    import habitus.cli as cli

    def boom(kind, city):
        raise RuntimeError(f"Overpass '{kind}' не удался: HTTP 504")

    monkeypatch.setattr(cli, "fetch_kind", boom)
    # run_offline теперь зовёт build_metro(fetch=fetch_system по умолчанию)
    # после enrich_all — без этой подмены тест реально ходил бы в Overpass
    # за метро/МЦК/МЦД (см. tests must not hit the network).
    monkeypatch.setattr(cli, "fetch_system", lambda system, city: [])
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        # no_ors=True (R50): без него, на машине с настроенным ORS_API_KEY,
        # build_metro(walker=ORSWalker()) реально уходил бы во внешний ORS —
        # fetch_osm=True здесь означает и метро, не только POI.
        stats = run_offline(FIX, conn, model=FakeModel(), fetch_osm=True,
                            geocoder=_no_network_geocoder, no_ors=True)
    assert stats["listings"] == 2       # цикл дошёл до конца
    assert stats["embedded"] == 2       # и эмбеддинги посчитаны
    assert stats["osm_failed"]          # но провал зафиксирован, а не скрыт
    # запись должна нести настоящий 504 конкретного kind/city, а не проглоченный
    # TypeError от несовпавшей сигнатуры стаба — иначе тест зелёный по ошибке
    assert len(stats["osm_failed"]) == len(POI_KINDS)
    for kind, entry in zip(POI_KINDS, stats["osm_failed"]):
        assert entry.startswith(f"{kind}/msk: ")
        assert "504" in entry
    # Фикс-раунд 1, пункт 5: этот стаб — просто lambda с сигнатурой
    # fetch_system, никакого реального поведения он не проверяет. Если
    # сигнатура fetch_system когда-нибудь разъедется со стабом, вызов
    # упадёт исключением, попадёт в общий except в build_metro и молча
    # осядет в stats["metro"]["failed"] — тест остался бы зелёным по
    # неправильной причине (тот же класс дефекта, что уже был в Задаче 2
    # с устаревшей сигнатурой стаба). Явная проверка на пустой failed
    # ловит именно это дрожание сигнатуры.
    assert stats["metro"]["failed"] == []


def test_build_metro_reports_failed_systems_without_dying(monkeypatch):
    calls = []

    def fake_fetch(system, city):
        calls.append((system, city))
        if system == "mcd":
            raise RuntimeError("Overpass 504")
        return []

    class FakeConn:
        def rollback(self): pass
        def commit(self): pass

    stats = build_metro(FakeConn(), "msk", fetch=fake_fetch)
    assert [c[0] for c in calls] == ["subway", "mck", "mcd"]
    # отказ одной системы не уносит остальные и не глотается молча
    assert any("mcd" in f for f in stats["failed"])
    assert "subway" not in " ".join(stats["failed"])


def _line(ref, system, names, lon0=37.60):
    return LineRaw(
        system=system, ref=ref, name=f"линия {ref}", colour="#EF161E",
        stations=[StationRaw(osm_id=2000 + i, name=n, lon=lon0 + i * 0.02,
                             lat=55.75)
                  for i, n in enumerate(names)],
        geometry=[[lon0 + i * 0.02, 55.75] for i in range(len(names))])


def test_build_metro_removes_lines_gone_from_osm():
    """R36: upsert_transit только апсертит — линию, пропавшую из OSM (закрыли
    участок, переразметили релейшен под другим ref), никто не удаляет. Без
    отдельного шага удаления такая линия жила бы в графе вечно и участвовала
    бы в маршрутизации как настоящая.

    Система (subway) в обоих снимках отвечает НЕПУСТЫМ списком — иначе сработала
    бы отдельная защита «пустой успешный fetch ничего не трогает» (см. build_metro),
    и это была бы уже не эта проверка."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        conn.commit()

        def fetch_two(system, city):
            if system == "subway":
                return [_line("1", "subway", ("A", "B", "C")),
                        _line("2", "subway", ("X", "Y"), lon0=37.70)]
            return []

        build_metro(conn, "msk", fetch=fetch_two, walker=None)
        refs = {r[0] for r in conn.execute(
            "SELECT ref FROM metro_line WHERE city='msk' AND system='subway'"
        ).fetchall()}
        assert refs == {"1", "2"}

        # Линия "2" пропала из следующего снимка OSM — subway всё ещё
        # отвечает непустым списком, просто без неё.
        def fetch_one(system, city):
            if system == "subway":
                return [_line("1", "subway", ("A", "B", "C"))]
            return []

        build_metro(conn, "msk", fetch=fetch_one, walker=None)
        refs = {r[0] for r in conn.execute(
            "SELECT ref FROM metro_line WHERE city='msk' AND system='subway'"
        ).fetchall()}
        assert refs == {"1"}


def test_build_metro_keeps_data_of_system_whose_fetch_failed():
    """Провал fetch у ОДНОЙ системы не должен стирать её прежние (валидные)
    данные — это отдельный риск от «линия пропала из OSM» (R36): здесь OSM
    вообще не ответил, значит текущее состояние неизвестно, а не пусто."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        conn.commit()

        def fetch_ok(system, city):
            if system == "subway":
                return [_line("1", "subway", ("A", "B", "C"))]
            if system == "mck":
                return [_line("14", "mck", ("X", "Y"))]
            return []

        build_metro(conn, "msk", fetch=fetch_ok, walker=None)

        def fetch_mck_down(system, city):
            if system == "mck":
                raise RuntimeError("Overpass 504")
            if system == "subway":
                return [_line("1", "subway", ("A", "B", "C"))]
            return []

        stats = build_metro(conn, "msk", fetch=fetch_mck_down, walker=None)
        assert any("mck" in f for f in stats["failed"])
        refs = {r[0] for r in conn.execute(
            "SELECT ref FROM metro_line WHERE city='msk'").fetchall()}
        # subway пересобрана штатно, mck (провалившийся fetch) сохранила
        # прежние данные — а не стала пустой из-за общего TRUNCATE.
        assert refs == {"1", "14"}


def test_build_metro_refuses_delete_missing_on_suspiciously_short_fetch():
    """R47 (фикс-раунд 1): усечённый (не пустой!) ответ Overpass — потерялись
    elements где-то в середине рекурсивного `>;` — раньше молча стирал линии,
    которых не было в укороченном списке: delete-missing сравнивал fetched refs
    с БД буквально, без всякой защиты на «подозрительно мало». Воспроизведено
    до фикса на реальном прогоне build_metro (3 линии → 1 в следующем fetch →
    2 линии стёрты, stats["failed"] == []); см. task-8-report.md, фикс-раунд 1,
    пункт 1, для протокола repro."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        conn.commit()

        def fetch_three(system, city):
            if system == "subway":
                return [_line("1", "subway", ("A", "B")),
                        _line("2", "subway", ("C", "D"), lon0=37.70),
                        _line("3", "subway", ("E", "F"), lon0=37.80)]
            return []

        build_metro(conn, "msk", fetch=fetch_three, walker=None)
        refs = {r[0] for r in conn.execute(
            "SELECT ref FROM metro_line WHERE city='msk' AND system='subway'"
        ).fetchall()}
        assert refs == {"1", "2", "3"}

        # 1 линия из 3 — меньше половины того, что лежит в БД: подозрительно
        # усечённый ответ, а не настоящее закрытие двух линий разом.
        def fetch_truncated(system, city):
            if system == "subway":
                return [_line("1", "subway", ("A", "B"))]
            return []

        stats = build_metro(conn, "msk", fetch=fetch_truncated, walker=None)
        refs = {r[0] for r in conn.execute(
            "SELECT ref FROM metro_line WHERE city='msk' AND system='subway'"
        ).fetchall()}
        # Все три линии на месте — delete-missing отменён.
        assert refs == {"1", "2", "3"}
        assert any("subway" in f and "msk" in f for f in stats["failed"])


def test_metro_cli_branch_uses_stubbed_fetch_and_never_hits_network(monkeypatch):
    """R52 (фикс-раунд 2): до фикса `elif args.cmd == "metro":` звал
    build_metro без fetch=fetch_system явно — полагаясь на дефолт параметра
    build_metro, захваченный ОДИН РАЗ при определении функции, а не на живой
    lookup имени в глобалах cli. monkeypatch.setattr(cli, "fetch_system", ...)
    такой дефолт не подменяет (тот же механизм, что уже потребовал
    fetch=fetch_system явно в вызове из run_offline, см. R52-комментарий в
    cli.py) — ветка `metro` была непроверяемой и реально стучалась в
    Overpass, что ре-ревьюер обнаружил на собственной пробе."""
    import habitus.cli as cli

    calls = []

    def fake_fetch(system, city):
        calls.append((system, city))
        return []

    monkeypatch.setattr(cli, "fetch_system", fake_fetch)
    monkeypatch.setattr(
        sys, "argv", ["habitus", "metro", "--city", "msk", "--no-ors"])

    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        conn.commit()

    cli.main()  # без SystemExit — все fetch пустые, но успешные

    assert [c[0] for c in calls] == ["subway", "mck", "mcd"]


def test_metro_cli_exits_nonzero_when_stats_failed_is_not_empty(monkeypatch):
    """R51 (фикс-раунд 2): отказ (в т.ч. срабатывание R47-порога) раньше был
    виден только в stdout — код возврата оставался 0, и cron/скриптовый
    раннер не мог отличить успешный прогон от отказавшего."""
    import habitus.cli as cli

    def fake_fetch(system, city):
        if system == "mcd":
            raise RuntimeError("Overpass 504")
        return []

    monkeypatch.setattr(cli, "fetch_system", fake_fetch)
    monkeypatch.setattr(
        sys, "argv", ["habitus", "metro", "--city", "msk", "--no-ors"])

    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        conn.commit()

    try:
        cli.main()
    except SystemExit as exc:
        code = exc.code
    else:
        code = None
    assert code == 1
