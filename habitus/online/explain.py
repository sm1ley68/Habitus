# habitus/online/explain.py — объяснение строго поверх фактов из БД
import json
from collections.abc import AsyncIterator

from habitus.config import settings
from habitus.online.llm import AsyncStreamLLMClient, LLMClient
from habitus.online.schema import ResultItem

GROUNDED_SYSTEM = """Ты — ассистент по недвижимости. Объясни пользователю подбор \
квартир по его запросу.
ЖЁСТКОЕ ПРАВИЛО: используй ТОЛЬКО данные из блока ФАКТЫ. Адрес и станцию метро \
называть можно — но строго теми значениями, что стоят в полях address и \
metro_station. Запрещено называть названия школ, ЖК, застройщиков и любую \
географию, которой нет в ФАКТАХ. Если каких-то данных нет — просто не упоминай их.
Если в ФАКТАХ есть строка «ОСЛАБЛЕНО», честно скажи, какие условия пришлось ослабить.
Если в ФАКТАХ есть строка «ПРИМЕЧАНИЕ», честно упомяни её.
Отвечай на языке запроса пользователя, кратко: 3-6 предложений."""


def facts_block(results: list[ResultItem], relaxed: list[str],
                notes: list[str] | None = None) -> str:
    """Факты для промпта: по JSON-строке на объект + строки ослаблений/примечаний.

    results режется до settings.rerank_top_n: /search теперь отдаёт запас до
    settings.result_max_n объектов для пагинации шлюза, а объяснение должно
    оставаться про то, что видит пользователь на первой странице, а не тащить
    в промпт весь запас.
    """
    lines = [json.dumps({"id": r.external_id, "price": r.price, "area": r.area,
                         "rooms": r.rooms, **r.address_facts}, ensure_ascii=False)
             for r in results[:settings.rerank_top_n]]
    if relaxed:
        lines.append("ОСЛАБЛЕНО: " + "; ".join(relaxed))
    if notes:
        lines.append("ПРИМЕЧАНИЕ: " + "; ".join(notes))
    return "\n".join(lines)


def template_explanation(results: list[ResultItem], relaxed: list[str]) -> str:
    """Деградация LLM: детерминированный ответ из тех же фактов."""
    if not results:
        return ("По заданным условиям ничего не найдено. "
                "Попробуйте ослабить фильтры.")
    parts = [f"Найдено объектов: {len(results)}."]
    top, f = results[0], results[0].address_facts
    bits = []
    if top.price is not None:
        bits.append(f"цена {top.price:,} ₽".replace(",", " "))
    if top.rooms is not None:
        bits.append(f"{top.rooms}-комн")
    if top.area is not None:
        bits.append(f"{top.area:.0f} м²")
    if f.get("walk_min_school") is not None:
        bits.append(f"школа в {f['walk_min_school']:.0f} мин пешком")
    if f.get("walk_min_metro") is not None:
        bits.append(f"метро в {f['walk_min_metro']:.0f} мин")
    if f.get("noise_level") == "low":
        bits.append("тихо")
    if f.get("bar_density_500m") == 0:
        bits.append("баров в радиусе 500 м нет")
    parts.append("Лучший вариант: " + ", ".join(bits) + ".")
    if relaxed:
        parts.append("Ослаблены условия: " + "; ".join(relaxed) + ".")
    return " ".join(parts)


def cache_key(query: str, results: list[ResultItem]) -> str:
    """Ключ кэша объяснений — общий для /search и /explain/stream, чтобы текст,
    посчитанный одним путём, доставался второму без повторного вызова LLM."""
    return query + "|" + ",".join(r.external_id for r in results)


def build_messages(query: str, results: list[ResultItem], relaxed: list[str],
                   notes: list[str] | None = None) -> list[dict]:
    """Промпт объяснения. Один и тот же для синхронного и потокового пути."""
    return [
        {"role": "system", "content": GROUNDED_SYSTEM},
        {"role": "user", "content":
         f"Запрос пользователя: {query}\n\nФАКТЫ:\n"
         f"{facts_block(results, relaxed, notes)}\n\nОбъясни подбор."},
    ]


async def explain_stream(query: str, results: list[ResultItem],
                         relaxed: list[str],
                         llm: AsyncStreamLLMClient | None,
                         notes: list[str] | None = None) -> AsyncIterator[dict]:
    """Поток объяснения: {"token": …} … затем {"done": True, "llm_ok": bool}.

    Обрыв ДО первого токена ничем не отличается от отсутствия LLM — отдаём
    шаблон. Обрыв ПОСЛЕ первого токена оставляем как есть: дописывать шаблон
    поверх уже показанного текста значит выдать пользователю два объяснения
    подряд. Деградация в обоих случаях честная — llm_ok=False.
    """
    delivered = False
    if llm is not None:
        try:
            async for chunk in llm.stream(
                    build_messages(query, results, relaxed, notes),
                    temperature=0.0):
                if not chunk:
                    continue
                delivered = True
                yield {"token": chunk}
        except Exception:
            if delivered:
                yield {"done": True, "llm_ok": False}
                return
        else:
            if delivered:
                yield {"done": True, "llm_ok": True}
                return

    yield {"token": template_explanation(results, relaxed)}
    yield {"done": True, "llm_ok": False}


def explain(query: str, results: list[ResultItem], relaxed: list[str],
            llm: LLMClient | None, notes: list[str] | None = None) -> tuple[str, bool]:
    """(текст, llm_ok). Любая ошибка LLM → шаблон, llm_ok=False."""
    if llm is None:
        return template_explanation(results, relaxed), False
    messages = build_messages(query, results, relaxed, notes)
    try:
        resp = llm.complete(messages, temperature=0.0)
        if resp.content:
            return resp.content, True
    except Exception:
        pass
    return template_explanation(results, relaxed), False
