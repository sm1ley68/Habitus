# tests/conftest.py — изоляция тестов от рабочей БД.
#
# Тесты делают TRUNCATE listings / DROP TABLE ... CASCADE. До этого файла они
# шли в settings.db_dsn — ту самую базу, где лежат демо-объявления, — и один
# прогон pytest стирал данные, восстановление которых стоит полного
# оффлайн-прогона с эмбеддингами.
#
# Здесь две гарантии:
#   1. settings.db_dsn подменяется на ОТДЕЛЬНУЮ базу (по умолчанию — имя
#      рабочей + суффикс _test) ещё на импорте conftest, до сбора тестов.
#      Тесты читают DSN в момент вызова, поэтому править их не нужно.
#   2. psycopg.connect оборачивается так, что недоступный Postgres даёт
#      pytest.skip с внятным текстом вместо OperationalError. Тогда чистый
#      клон без поднятой БД показывает «passed + skipped», а не стену красного.
import os
from urllib.parse import urlsplit, urlunsplit

import psycopg
import pytest
from psycopg.conninfo import conninfo_to_dict, make_conninfo

from habitus.config import settings

#: DSN рабочей базы — запоминаем ДО подмены, чтобы было с чем сравнивать.
PROD_DSN = settings.db_dsn


def _derive_test_dsn(prod_dsn: str) -> str:
    """Тот же сервер и та же учётка, другая база: habitus → habitus_test.

    Форма DSN сохраняется: URL остаётся URL-ом. Иначе всё, что смотрит на DSN
    как на строку (например, проверка схемы в test_config), видит другой формат.
    """
    test_db = (conninfo_to_dict(prod_dsn).get("dbname") or "habitus") + "_test"
    if "://" in prod_dsn:
        return urlunsplit(urlsplit(prod_dsn)._replace(path="/" + test_db))
    return make_conninfo(prod_dsn, dbname=test_db)


TEST_DSN = os.getenv("HABITUS_TEST_DSN") or _derive_test_dsn(PROD_DSN)

# Явная защита от «переопределил переменную и снова стёр демо-базу».
if conninfo_to_dict(TEST_DSN).get("dbname") == conninfo_to_dict(PROD_DSN).get("dbname"):
    raise RuntimeError(
        "HABITUS_TEST_DSN указывает на рабочую базу "
        f"({conninfo_to_dict(PROD_DSN).get('dbname')}). Тесты делают TRUNCATE и "
        "DROP TABLE — укажите отдельную базу."
    )

# Подмена до сбора тестов: ни один модуль не успеет прочитать рабочий DSN.
settings.db_dsn = TEST_DSN

_real_connect = psycopg.connect


def _connect_or_skip(conninfo="", **kwargs):
    """psycopg.connect, который на недоступной БД пропускает тест, а не роняет.

    Скипается ровно тот тест, который полез в базу: тесты без БД
    (парсинг, схемы, кэш, ранжирование) продолжают выполняться как обычно.
    """
    try:
        return _real_connect(conninfo, **kwargs)
    except psycopg.OperationalError as exc:
        pytest.skip(f"Postgres недоступен ({TEST_DSN}): {exc}")


psycopg.connect = _connect_or_skip


def _create_test_database() -> None:
    """CREATE DATABASE <...>_test, если её ещё нет. Молча выходим, если сервер
    недоступен — конкретные тесты всё равно скипнутся в _connect_or_skip."""
    target = conninfo_to_dict(TEST_DSN)
    dbname = target.pop("dbname")
    admin_dsn = make_conninfo(**target, dbname="postgres")
    try:
        with _real_connect(admin_dsn, autocommit=True) as admin:
            exists = admin.execute(
                "SELECT 1 FROM pg_database WHERE datname = %s", (dbname,)
            ).fetchone()
            if not exists:
                # Имя из conninfo, не из пользовательского ввода; идентификатор
                # нельзя передать биндом, поэтому экранируем кавычками.
                admin.execute(f'CREATE DATABASE "{dbname}"')
    except psycopg.Error:
        return


@pytest.fixture(scope="session", autouse=True)
def _test_database() -> None:
    _create_test_database()
