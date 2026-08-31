# habitus/online/nlu.py — Linguistic Agent: свободный текст → ParsedQuery /
# ParsedTurn (намерение реплики + разбор с учётом предыдущего шага диалога)
from typing import TypeVar
from pydantic import BaseModel, ValidationError
from habitus.online.llm import LLMClient
from habitus.online.schema import ParsedQuery, ParsedTurn

M = TypeVar("M", bound=BaseModel)


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
- Сторона города или район/место поиска → поле area (НЕ в semantic_text): \
«на севере (москвы)» → area="север"; «юго-запад» → area="юго-запад"; \
«в центре» → area="центр"; «рядом со Сколково», «у Патриарших» → area="Сколково" / \
area="Патриаршие пруды". Только сторона света ИЛИ одно место; вайб оставляй в \
semantic_text.
Примеры area: «в Хамовниках»→"Хамовники"; «в центре»→"центр"; «внутри Садового»→\
"внутри Садового"; «у Патриков»→"Патрики"; «northern Moscow»→"север".
- Состав семьи и поездки добавляй в household только когда человек и место явно \
названы. id — короткий латинский slug, label — исходное обозначение («Сын», «Жена»).
- В household.legs не придумывай время: depart/arrive заполняй только если время \
есть в запросе. mode по умолчанию walk допустим только для явно пешей поездки; \
иначе используй явно названный режим.
- to_kind ∈ school|metro|work|park|poi; mode ∈ walk|scooter|bus|car|metro.
- Бюджет поездки на метро («40 минут до работы на метро», «без машины, час до \
центра») — это не household, а отдельная поездка до места: назови место в \
to_label ноги household с mode "metro". Если места нет, а есть только режим \
(«без машины»), ставь mode "metro" у уже названных поездок, ничего не выдумывая. \
Поле point здесь не трогай: это отдельный параметр SearchRequest, задаётся \
явно вызывающей стороной, а не разбором текста — «метро» в запросе не повод \
его синтезировать.
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

Запрос: «двушка на севере москвы в тихом районе с парками, гулять с собакой»
→ {"rooms": [2], "area": "север", "noise_max": "low", "geo": [{"kind": "park", \
"walk_minutes": 15}], "semantic_text": "прогулки с собакой", "lang": "ru"}

Запрос: "quiet flat near a strong school, no bars around"
→ {"geo": [{"kind": "school", "walk_minutes": 15}], "noise_max": "low", \
"stop_factors": ["bars"], "semantic_text": "quiet flat near a strong school", \
"lang": "en"}

Запрос: «двушка, без машины, до Сити не больше 40 минут на метро»
→ {"rooms": [2], "household": [{"id": "me", "label": "я", "legs": \
[{"to_label": "Москва-Сити", "to_kind": "work", "mode": "metro"}]}]}
"""

PARSE_TOOL = {
    "type": "function",
    "function": {
        "name": "submit_parsed_query",
        "description": "Структурированный разбор запроса по недвижимости",
        "parameters": ParsedQuery.model_json_schema(),
    },
}

# Блок системного промпта, добавляемый только когда есть предыдущий разбор
# (многоходовый чат). {prev_json} — ParsedQuery предыдущего шага диалога.
TURN_PROMPT_SUFFIX = """

Это не первая реплика диалога. Вот структурированный разбор ПРЕДЫДУЩЕГО запроса \
пользователя (JSON ParsedQuery):
{prev_json}

Классифицируй текущую реплику полем intent и вызови submit_parsed_turn:
- "new_search" — человек ищет другое: сменились комнаты/район/бюджет так, что \
это уже другой запрос, либо явно попросили начать заново («давай заново», \
«забудь, ищем другое»). В query — полный новый разбор реплики (как обычно), \
cleared_fields не нужен.
- "refine" — человек правит предыдущий запрос: «подешевле», «только с метро \
ближе», «убери шумные», «и с парком рядом». В query кладутся ТОЛЬКО поля, \
которые реплика меняет или добавляет (остальные оставь дефолтными — они не \
трогаются). Ограничения, которые пользователь явно снимает («без бюджета», \
«неважно про метро», «убери фильтр по шуму») — не в query, а в cleared_fields \
списком имён полей ParsedQuery (например ["price_max"], ["noise_max"]).
- "followup" — реплика не меняет параметры поиска, это вопрос про уже \
показанную выдачу («а какой у первого этаж», «расскажи про второй вариант»). \
Новый поиск не нужен: query оставь пустым (все поля дефолтные), \
cleared_fields — пустой список.
"""

TURN_TOOL = {
    "type": "function",
    "function": {
        "name": "submit_parsed_turn",
        "description": "Намерение реплики чата + разбор относительно "
                       "предыдущего запроса",
        "parameters": ParsedTurn.model_json_schema(),
    },
}


def _complete_with_retries(messages: list[dict], tool: dict,
                           model_cls: type[M], llm: LLMClient,
                           max_retries: int) -> M:
    """Общий цикл parse_query/parse_turn: вызов LLM с tool-схемой,
    невалидный ответ — текст ошибки обратно модели."""
    last_err = ""
    for _ in range(max_retries):
        resp = llm.complete(messages, tools=[tool], temperature=0.0)
        raw = resp.tool_arguments or resp.content or ""
        try:
            return model_cls.model_validate_json(raw)
        except ValidationError as e:
            last_err = str(e)
            messages.append({"role": "assistant", "content": raw})
            messages.append({"role": "user", "content":
                             f"Ответ не прошёл валидацию схемы: {last_err}\n"
                             f"Верни исправленный JSON строго по схеме "
                             f"{tool['function']['name']}."})
    raise ParseError(f"NLU: нет валидного {model_cls.__name__} за {max_retries} "
                     f"попыток: {last_err}")


def parse_query(text: str, llm: LLMClient, max_retries: int = 3) -> ParsedQuery:
    """Вызов LLM с tool-схемой; невалидный ответ → текст ошибки обратно модели.

    Используется eval-раннером и там, где предыдущего шага диалога нет —
    для многоходового чата см. parse_turn."""
    messages = [{"role": "system", "content": SYSTEM_PROMPT},
                {"role": "user", "content": text}]
    return _complete_with_retries(messages, PARSE_TOOL, ParsedQuery, llm,
                                  max_retries)


def parse_turn(text: str, llm: LLMClient, prev: ParsedQuery | None,
               max_retries: int = 3) -> ParsedTurn:
    """Разбор реплики чата с классификацией намерения.

    prev is None — предыдущего разбора в диалоге ещё нет, классифицировать
    нечего: intent всегда "new_search", а query получается тем же промптом и
    той же tool-схемой, что и в parse_query (поведение эквивалентно ей).
    prev задан — в промпт добавляется его JSON и правила new_search/refine/
    followup (см. TURN_PROMPT_SUFFIX), модель зовёт submit_parsed_turn.
    """
    if prev is None:
        messages = [{"role": "system", "content": SYSTEM_PROMPT},
                    {"role": "user", "content": text}]
        pq = _complete_with_retries(messages, PARSE_TOOL, ParsedQuery, llm,
                                    max_retries)
        return ParsedTurn(intent="new_search", query=pq, cleared_fields=[])

    system_prompt = SYSTEM_PROMPT + TURN_PROMPT_SUFFIX.format(
        prev_json=prev.model_dump_json())
    messages = [{"role": "system", "content": system_prompt},
                {"role": "user", "content": text}]
    return _complete_with_retries(messages, TURN_TOOL, ParsedTurn, llm,
                                  max_retries)


def merge_parsed(prev: ParsedQuery, turn: ParsedTurn) -> ParsedQuery:
    """Слияние предыдущего разбора с разбором текущей реплики.

    Правила (см. бриф задачи):
    - intent == "new_search" → turn.query целиком, prev не участвует.
    - иначе — prev как база; поверх накладываются поля turn.query с
      содержательным значением (не None и не пустые список/строка); поля из
      turn.cleared_fields сбрасываются в дефолт ParsedQuery; semantic_text —
      непустой новый заменяет старый, пустой сохраняет старый; lang заменяется
      только если реплика прислала его явно.
    """
    if turn.intent == "new_search":
        return turn.query

    defaults = ParsedQuery().model_dump()
    merged = prev.model_dump()
    turn_fields = turn.query.model_dump()

    for name, value in turn_fields.items():
        if name in ("semantic_text", "lang"):
            continue      # у этих полей своё правило — обрабатываются ниже
        # Пустое значение — это не «сбросить ограничение»: для сброса есть
        # cleared_fields. Иначе `{"rooms": []}` от LLM молча снял бы фильтр.
        if value is None or value == [] or value == "":
            continue
        merged[name] = value

    if turn_fields["semantic_text"]:
        merged["semantic_text"] = turn_fields["semantic_text"]

    # Промпт правки велит присылать только изменившиеся поля, поэтому дефолтный
    # "ru" в разборе реплики означает «языка не было», а не «переключись на ru».
    if "lang" in turn.query.model_fields_set:
        merged["lang"] = turn_fields["lang"]

    for field_name in turn.cleared_fields:
        merged[field_name] = defaults[field_name]

    return ParsedQuery.model_validate(merged)
