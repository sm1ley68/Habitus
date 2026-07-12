# habitus/online/explain.py — объяснение строго поверх фактов из БД
import json

from habitus.online.llm import LLMClient
from habitus.online.schema import ResultItem

GROUNDED_SYSTEM = """Ты — ассистент по недвижимости. Объясни пользователю подбор \
квартир по его запросу.
ЖЁСТКОЕ ПРАВИЛО: используй ТОЛЬКО данные из блока ФАКТЫ. Запрещено называть адреса, \
районы, станции метро, названия школ и любые сведения, которых нет в ФАКТАХ. \
Если каких-то данных нет — просто не упоминай их.
Если в ФАКТАХ есть строка «ОСЛАБЛЕНО», честно скажи, какие условия пришлось ослабить.
Отвечай на языке запроса пользователя, кратко: 3-6 предложений."""


def facts_block(results: list[ResultItem], relaxed: list[str]) -> str:
    """Факты для промпта: по JSON-строке на объект + строка ослаблений."""
    lines = [json.dumps({"id": r.external_id, "price": r.price, "area": r.area,
                         "rooms": r.rooms, **r.address_facts}, ensure_ascii=False)
             for r in results]
    if relaxed:
        lines.append("ОСЛАБЛЕНО: " + "; ".join(relaxed))
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


def explain(query: str, results: list[ResultItem], relaxed: list[str],
            llm: LLMClient | None) -> tuple[str, bool]:
    """(текст, llm_ok). Любая ошибка LLM → шаблон, llm_ok=False."""
    if llm is None:
        return template_explanation(results, relaxed), False
    messages = [
        {"role": "system", "content": GROUNDED_SYSTEM},
        {"role": "user", "content":
         f"Запрос пользователя: {query}\n\nФАКТЫ:\n"
         f"{facts_block(results, relaxed)}\n\nОбъясни подбор."},
    ]
    try:
        resp = llm.complete(messages, temperature=0.0)
        if resp.content:
            return resp.content, True
    except Exception:
        pass
    return template_explanation(results, relaxed), False
