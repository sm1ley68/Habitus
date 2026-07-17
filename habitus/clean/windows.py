# habitus/clean/windows.py — сторона света окон из прозы description (LLM, temp=0)
# Принцип тот же, что во всём досье: НИКАКИХ догадок — извлекаем только явно
# написанное. Детерминированная астрономия разрешена (закат→W, рассвет→E),
# «солнечная сторона» и подобное — НЕ направление, пропускаем.
import re
from typing import Literal

import psycopg
from pydantic import BaseModel, ValidationError

from habitus.online.llm import LLMClient

# префильтр: LLM зовём только там, где в прозе вообще есть намёк на окна/свет
MENTION_REGEX = r"окн|закат|рассвет|сторон[уыае]? свет"

# --- лексический гейт: направление засчитывается только при якоре в тексте ---
# LLM понимает контекст, но местами игнорирует запрет на вывод («в сторону
# Кремля» → S,W — галлюцинация географии). Гейт детерминированно требует
# явного слова; составные проверяются ДО одиночных и вырезаются, чтобы
# «юго-запад» не засчитал S и W по подстрокам.
_COMPOUND = [(r"юго[\s-]*запад", "SW"), (r"юго[\s-]*восто", "SE"),
             (r"северо[\s-]*запад", "NW"), (r"северо[\s-]*восто", "NE")]
_SINGLE = [(r"юг|южн", "S"), (r"север", "N"), (r"запад", "W"),
           (r"восто|восход|рассвет", "E"), (r"закат|заход[а-я]* солнца", "W")]


def lexical_support(text: str) -> set[str]:
    """Какие направления имеют явный лексический якорь в тексте."""
    t = text.lower()
    supported: set[str] = set()
    for pattern, direction in _COMPOUND:
        if re.search(pattern, t):
            supported.add(direction)
            t = re.sub(pattern, " ", t)     # чтобы одиночные не сработали по куску
    for pattern, direction in _SINGLE:
        if re.search(pattern, t):
            supported.add(direction)
    return supported


class WindowParseError(RuntimeError):
    """Не удалось получить валидный список направлений за max_retries попыток."""


class WindowOrientation(BaseModel):
    window_orientation: list[
        Literal["N", "NE", "E", "SE", "S", "SW", "W", "NW"]] = []


SYSTEM_PROMPT = """Ты извлекаешь сторону света окон из описания квартиры. \
Вызови инструмент submit_window_orientation.

Правила — строго:
- Заполняй ТОЛЬКО если направление явно написано в тексте. Ничего не угадывай.
- «окна на юг» → ["S"]; «на юго-запад» → ["SW"]; «на север и восток» → ["N","E"].
- Детерминированная астрономия разрешена: «закат из окон» → ["W"], \
«рассвет» → ["E"].
- «солнечная сторона», «светлая квартира», «окна во двор/на улицу» — это НЕ \
сторона света. В таких случаях верни пустой список.
- Направления только из enum: N, NE, E, SE, S, SW, W, NW.

Примеры:
«Окна выходят на юго-запад, видовая» → {"window_orientation": ["SW"]}
«Тихий двор, окна во двор» → {"window_orientation": []}
«Из спальни виден рассвет, из гостиной — закат» → {"window_orientation": ["E","W"]}
"""

WINDOW_TOOL = {
    "type": "function",
    "function": {
        "name": "submit_window_orientation",
        "description": "Явно указанная в тексте сторона света окон",
        "parameters": WindowOrientation.model_json_schema(),
    },
}


def parse_windows(text: str, llm: LLMClient, max_retries: int = 3) -> list[str]:
    """Проза → список направлений (может быть пуст). Невалидный ответ →
    текст ошибки обратно модели (та же петля самопочинки, что в NLU)."""
    messages = [{"role": "system", "content": SYSTEM_PROMPT},
                {"role": "user", "content": text}]
    last_err = ""
    for _ in range(max_retries):
        resp = llm.complete(messages, tools=[WINDOW_TOOL], temperature=0.0)
        raw = resp.tool_arguments or resp.content or ""
        try:
            return WindowOrientation.model_validate_json(raw).window_orientation
        except ValidationError as e:
            last_err = str(e)
            messages.append({"role": "assistant", "content": raw})
            messages.append({"role": "user", "content":
                             f"Ответ не прошёл валидацию схемы: {last_err}\n"
                             f"Верни исправленный JSON строго по схеме "
                             f"submit_window_orientation."})
    raise WindowParseError(f"нет валидного ответа за {max_retries} попыток: "
                           f"{last_err}")


def extract_windows(conn: psycopg.Connection, llm: LLMClient,
                    limit: int | None = None) -> dict:
    """Батч по listings: строки с прозой про окна и пустой window_orientation.
    Успешная экстракция пишет колонку; пустой результат и провал парса
    оставляют NULL (перепроверятся при следующем прогоне). Коммит на строку —
    длинный LLM-батч не должен терять сделанное при обрыве."""
    with conn.cursor() as cur:
        cur.execute(
            "SELECT external_id, description FROM listings "
            "WHERE is_active AND description ~* %s "
            "AND (window_orientation IS NULL OR window_orientation = '{}') "
            "ORDER BY external_id" + (" LIMIT %s" if limit else ""),
            [MENTION_REGEX] + ([limit] if limit else []))
        rows = cur.fetchall()
    checked = extracted = 0
    for eid, desc in rows:
        checked += 1
        try:
            dirs = parse_windows(desc, llm)
        except WindowParseError:
            continue
        # гейт: оставляем только направления с явным якорем в тексте
        dirs = [d for d in dirs if d in lexical_support(desc)]
        if not dirs:
            continue
        with conn.cursor() as cur:
            cur.execute("UPDATE listings SET window_orientation = %s, "
                        "updated_at = now() WHERE external_id = %s;",
                        (dirs, eid))
        conn.commit()
        extracted += 1
    return {"checked": checked, "extracted": extracted}
