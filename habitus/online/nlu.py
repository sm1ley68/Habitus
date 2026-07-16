# habitus/online/nlu.py — Linguistic Agent: свободный текст → ParsedQuery
from pydantic import ValidationError
from habitus.online.llm import LLMClient
from habitus.online.schema import ParsedQuery


class ParseError(RuntimeError):
    """NLU не смог получить валидный ParsedQuery за max_retries попыток."""


SYSTEM_PROMPT = """Ты — парсер запросов по недвижимости Москвы. Извлеки из запроса \
пользователя ТОЛЬКО явно указанные ограничения и вызови инструмент submit_parsed_query.

Правила:
- Не выдумывай значения: поле заполняется, только если оно явно есть в запросе.
- Жёсткие числовые/категориальные условия → поля фильтров; атмосфера и образы \
(«двор-колодец», «сталинка», «видовая») → semantic_text.
- «бюджет бизнес» ≈ price_max 40000000; «эконом» ≈ price_max 15000000 (Москва, рубли).
- Стороны света: юго-запад → ["SW"], запад → ["W"], юг → ["S"] и т.п.
- «тихо», «не шумно» → noise_max="low". «без баров» → stop_factors=["bars"].
- «рядом/near» без числа минут → walk_minutes 15.
- Состав семьи и поездки добавляй в household только когда человек и место явно \
названы. id — короткий латинский slug, label — исходное обозначение («Сын», «Жена»).
- В household.legs не придумывай время: depart/arrive заполняй только если время \
есть в запросе. mode по умолчанию walk допустим только для явно пешей поездки; \
иначе используй явно названный режим.
- to_kind ∈ school|metro|work|park|poi; mode ∈ walk|scooter|bus|car|metro.
- Запрос на английском языке → те же поля; semantic_text оставь на языке запроса, \
lang="en".

Примеры:
Запрос: «двушка или трёшка до 20 млн, школа в 10 минутах пешком, окна на юго-запад»
→ {"price_max": 20000000, "rooms": [2, 3], "geo": [{"kind": "school", \
"walk_minutes": 10}], "window_orientation": ["SW"], "semantic_text": "", "lang": "ru"}

Запрос: «работаем в Сколково и в Сити, нужен компромисс, тихий двор без баров»
→ {"noise_max": "low", "stop_factors": ["bars"], \
"semantic_text": "компромисс между Сколково и Сити, тихий двор", "lang": "ru"}

Запрос: «сын выходит в 08:15 и идёт пешком в лицей 239»
→ {"semantic_text": "", "lang": "ru", "household": [{"id": "son", \
"label": "Сын", "legs": [{"to_label": "Лицей 239, Москва", \
"to_kind": "school", "mode": "walk", "depart": "08:15"}]}]}

Запрос: "quiet flat near a strong school, no bars around"
→ {"geo": [{"kind": "school", "walk_minutes": 15}], "noise_max": "low", \
"stop_factors": ["bars"], "semantic_text": "quiet flat near a strong school", \
"lang": "en"}
"""

PARSE_TOOL = {
    "type": "function",
    "function": {
        "name": "submit_parsed_query",
        "description": "Структурированный разбор запроса по недвижимости",
        "parameters": ParsedQuery.model_json_schema(),
    },
}


def parse_query(text: str, llm: LLMClient, max_retries: int = 3) -> ParsedQuery:
    """Вызов LLM с tool-схемой; невалидный ответ → текст ошибки обратно модели."""
    messages = [{"role": "system", "content": SYSTEM_PROMPT},
                {"role": "user", "content": text}]
    last_err = ""
    for _ in range(max_retries):
        resp = llm.complete(messages, tools=[PARSE_TOOL], temperature=0.0)
        raw = resp.tool_arguments or resp.content or ""
        try:
            return ParsedQuery.model_validate_json(raw)
        except ValidationError as e:
            last_err = str(e)
            messages.append({"role": "assistant", "content": raw})
            messages.append({"role": "user", "content":
                             f"Ответ не прошёл валидацию схемы: {last_err}\n"
                             f"Верни исправленный JSON строго по схеме "
                             f"submit_parsed_query."})
    raise ParseError(f"NLU: нет валидного ParsedQuery за {max_retries} попыток: "
                     f"{last_err}")
