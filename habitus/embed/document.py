import hashlib


def _plural_rooms(n) -> str:
    return f"{n}-комн" if n else "студия/н.д."


def build_doc_text(row: dict) -> str:
    parts = []
    if row.get("description"):
        parts.append(row["description"].strip())
    parts.append(_plural_rooms(row.get("rooms")))
    if row.get("area"):
        parts.append(f"{row['area']:.0f} м²")
    if row.get("level") and row.get("levels"):
        parts.append(f"{row['level']}/{row['levels']} этаж")
    wo = row.get("window_orientation")
    if wo:
        parts.append("окна " + "/".join(wo))
    ws = row.get("walk_min_school")
    if ws is not None:
        parts.append(f"школа в {ws:.0f} мин пешком")
    wm = row.get("walk_min_metro")
    if wm is not None:
        parts.append(f"метро в {wm:.0f} мин пешком")
    wp = row.get("walk_min_park")
    if wp is not None:
        parts.append(f"парк в {wp:.0f} мин пешком")
    bd = row.get("bar_density_500m")
    if bd is not None:
        parts.append("баров в 500 м нет" if bd == 0 else f"баров рядом: {bd}")
    if row.get("noise_level"):
        parts.append("тихо" if row["noise_level"] == "low" else "шумно")
    return ", ".join(parts)


def content_hash(text: str) -> str:
    return hashlib.sha1(text.encode("utf-8")).hexdigest()
