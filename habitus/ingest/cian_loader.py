# habitus/ingest/cian_loader.py
import csv
import json
from pathlib import Path

# Циановский CSV (выхлоп Go-парсера бэка) уже содержит проза-`description` и
# стабильный `cian_id`, поэтому хешировать ключ, как в kaggle_loader, не нужно.
# Специфичные для Циана поля (zhk, metro[], building_material) сознательно НЕ
# переносим: их нет в схеме raw_listings/listings, а ЖК/метро/район почти всегда
# уже назван в самом description, откуда их и вытянет эмбеддинг BGE-M3.


def _to_int(v):
    try:
        return int(float(v))
    except (ValueError, TypeError):
        return None


def _to_float(v):
    try:
        return float(v)
    except (ValueError, TypeError):
        return None


# Циан отдаёт станции как JSON-массив {name, time, transport_type}. Приводим к
# общей форме {name, minutes, mode}: у следующего источника (Дубай) поля будут
# называться иначе, а promote_to_listings должен читать одинаково.
def parse_metro(raw: str | None) -> list[dict]:
    if not raw or not str(raw).strip():
        return []
    try:
        items = json.loads(raw)
    except (ValueError, TypeError):
        return []
    if not isinstance(items, list):
        return []
    out = []
    for it in items:
        if not isinstance(it, dict):
            continue
        minutes, name = _to_int(it.get("time")), (it.get("name") or "").strip()
        if minutes is None or not name:
            continue
        mode = "walk" if it.get("transport_type") == "walk" else "transport"
        out.append({"name": name, "minutes": minutes, "mode": mode})
    return out


def parse_csv(path: Path) -> list[dict]:
    out = []
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            cian_id = (row.get("cian_id") or "").strip()
            if not cian_id:
                continue
            out.append({
                "external_id": f"cian_{cian_id}",
                "source": "cian",
                "price": _to_int(row.get("price")),
                "area": _to_float(row.get("area")),
                "kitchen_area": None,          # в Циановском выхлопе нет
                "rooms": _to_int(row.get("rooms")),
                "level": _to_int(row.get("floor")),
                "levels": _to_int(row.get("floors")),
                "building_type": None,         # material — строковый enum, не int-код
                "object_type": None,
                "lat": _to_float(row.get("latitude")),
                "lon": _to_float(row.get("longitude")),
                "description": (row.get("description") or "").strip() or None,
                "city": "msk",
                "address": (row.get("address") or "").strip() or None,
                "source_url": (row.get("url") or "").strip() or None,
                "source_extra": {
                    "metro": parse_metro(row.get("metro")),
                    "zhk": (row.get("zhk") or "").strip() or None,
                    "building_material": (row.get("building_material") or "").strip() or None,
                    "deadline": (row.get("deadline") or "").strip() or None,
                },
            })
    return out
