# habitus/online/errors.py — перевод падения пайплайна в структурный отказ.
#
# До этого модуля любое исключение внутри /search доезжало до шлюза как
# дефолтный FastAPI-шный 500 с телом {"detail": "Internal Server Error"}:
# причина оставалась в логе ML-контейнера, а шлюз и пользователь видели одну и
# ту же пустую строку и для «Postgres не поднят», и для «у OpenRouter кончилась
# квота», и для «таблицы listings нет, оффлайн-фаза не прогонялась». Диагноз
# терялся ровно там, где он был известен.
#
# Здесь исключение превращается в code + причину + подсказку «что чинить», а
# трейсинг добавляет стадию, на которой рвануло, и тайминги стадий, успевших
# отработать до неё. Шлюз кладёт это в поля cause/hint своего конверта ошибки.
import logging
import re
from contextlib import contextmanager
from urllib.parse import urlsplit

import psycopg
from fastapi import HTTPException

from habitus.config import settings
from habitus.online import trace
from habitus.online.llm import LLMUnavailable
from habitus.online.nlu import ParseError

log = logging.getLogger("habitus.errors")


def _dsn_target() -> str:
    """host:port/база из рабочего DSN — без логина и пароля.

    Подсказка обязана назвать, КУДА сервис не достучался: половина отказов на
    dev-машине — это «Postgres слушает 5432, а DSN смотрит в 5544». Но DSN
    несёт пароль, и в тело HTTP-ответа он попасть не может.
    """
    try:
        parts = urlsplit(settings.db_dsn)
        host = parts.hostname or "localhost"
        port = f":{parts.port}" if parts.port else ""
        db = parts.path.lstrip("/")
        return f"{host}{port}/{db}" if db else f"{host}{port}"
    except ValueError:
        return "адрес из DB_DSN"


# Пароль в DSN: psycopg охотно печатает строку подключения целиком в тексте
# OperationalError, а этот текст едет наружу как причина отказа. Логин
# оставляем — он часть диагноза («ходим не тем пользователем»).
_DSN_CREDENTIALS = re.compile(r"(postgres(?:ql)?://[^:/@\s]+):[^@\s]*@")


def _redact(text: str) -> str:
    return _DSN_CREDENTIALS.sub(r"\1:***@", text)


def _reason(exc: Exception) -> str:
    """Человеческий текст исключения: без него причина — пустой звук."""
    return _redact(str(exc).strip())


# Порядок важен: UndefinedTable/UndefinedFunction — подклассы psycopg.Error,
# и generic-ветка про базу обязана проверяться последней.
def describe_failure(exc: Exception) -> dict:
    """Классифицировать падение: код, причина, что чинить, стадия, тайминги.

    Возвращает dict, который эндпоинт кладёт в detail HTTPException. Ключ
    status — какой HTTP отдать: 503 для отказа внешней зависимости (её чинят
    снаружи и запрос имеет смысл повторить), 500 для всего остального.
    """
    ctx = trace.failure_context(exc)
    base = {"stage": ctx.get("stage", ""), "timings": ctx.get("timings", {})}

    if isinstance(exc, LLMUnavailable):
        return {**base, "status": 503, "code": "llm_unavailable",
                "message": f"LLM не ответила: {_reason(exc)}",
                "hint": "Проверьте OPENROUTER_API_KEY и остаток квоты у "
                        "openrouter.ai — вся цепочка моделей "
                        f"({settings.llm_model} + фолбэки) вернула отказ"}

    if isinstance(exc, ParseError):
        return {**base, "status": 503, "code": "nlu_parse_failed",
                "message": f"Разбор запроса не удался: {_reason(exc)}",
                "hint": "Модель не отдала валидный JSON за отведённые попытки — "
                        "смотрите логи стадии parse; запрос стоит повторить"}

    if isinstance(exc, (psycopg.errors.UndefinedTable,
                        psycopg.errors.UndefinedColumn)):
        return {**base, "status": 500, "code": "db_schema_missing",
                "message": f"Схема базы не совпадает с кодом: {_reason(exc)}",
                "hint": "Не прогонялась оффлайн-фаза или база отстала от кода: "
                        "uv run habitus offline --csv <path>"}

    if isinstance(exc, (psycopg.errors.UndefinedFunction,
                        psycopg.errors.UndefinedObject)):
        return {**base, "status": 500, "code": "db_extension_missing",
                "message": f"База не знает нужной функции или типа: {_reason(exc)}",
                "hint": "Похоже, не установлены расширения PostGIS / pgvector — "
                        "поднимайте базу из Dockerfile.db, а не голый postgres"}

    if isinstance(exc, psycopg.errors.InsufficientPrivilege):
        return {**base, "status": 500, "code": "db_permission_denied",
                "message": f"Базе не хватило прав: {_reason(exc)}",
                "hint": f"Пользователь из DB_DSN не имеет доступа к {_dsn_target()}"}

    if isinstance(exc, psycopg.OperationalError):
        return {**base, "status": 503, "code": "db_unavailable",
                "message": f"Нет связи с базой: {_reason(exc)}",
                "hint": f"Postgres по адресу {_dsn_target()} не отвечает — "
                        "поднят ли контейнер базы и совпадает ли порт в DB_DSN"}

    if isinstance(exc, psycopg.Error):
        return {**base, "status": 500, "code": "db_error",
                "message": f"Ошибка базы ({type(exc).__name__}): {_reason(exc)}",
                "hint": f"Запрос к {_dsn_target()} не выполнился — "
                        "подробности с SQL в логах ML-сервиса"}

    if isinstance(exc, MemoryError):
        return {**base, "status": 503, "code": "out_of_memory",
                "message": "Сервису не хватило памяти",
                "hint": "Уменьшите RERANK_POOL_N / EMBED_BATCH_SIZE или "
                        "дайте контейнеру больше памяти"}

    # Класс исключения — половина диагноза: «ValueError» и «KeyError» на одной
    # стадии чинятся по-разному, а общий текст «внутренняя ошибка» их равняет.
    return {**base, "status": 500, "code": "internal_error",
            "message": f"{type(exc).__name__}: {_reason(exc)}" if _reason(exc)
                       else type(exc).__name__,
            "hint": "Непредвиденный отказ — трассировка в логах ML-сервиса"}


@contextmanager
def pipeline_failures(endpoint: str):
    """Обёртка эндпоинта: падение пайплайна → HTTPException со структурным detail.

    HTTPException пропускается насквозь: 404 досье и 422 объявления продавца —
    это осознанные ответы, а не отказ пайплайна, и переклеивать им код нельзя.
    """
    try:
        yield
    except HTTPException:
        raise
    except Exception as exc:
        detail = describe_failure(exc)
        # exc_info — трассировка остаётся в логе целиком: наружу едет диагноз,
        # внутрь — доказательство.
        log.error("отказ %s: code=%s stage=%s %s", endpoint, detail["code"],
                  detail["stage"] or "—", detail["message"], exc_info=True)
        raise HTTPException(status_code=detail.pop("status"),
                            detail=detail) from exc
