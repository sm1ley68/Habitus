import hashlib


def _plural_rooms(n) -> str:
    return f"{n}-комн" if n else "студия/н.д."


def build_doc_text(row: dict) -> str:
    """Структурные факты идут ПЕРЕД описанием.

    Кросс-энкодер реранкера обрезает пару по rerank_max_length (512 токенов), а
    одно описание занимает медианно 428. Пока проза стояла первой, адрес доезжал
    до реранкера лишь у 69% объектов, а метро/школа/шум — и того реже: сильные
    структурные сигналы просто не попадали в окно. Теперь режется хвост прозы,
    а не факты. Для эмбеддинга порядок безразличен — BGE-M3 берёт до 8192 токенов
    и наши документы целиком в него влезают.
    """
    parts = []
    if row.get("address"):
        parts.append(row["address"].strip())
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
    if row.get("metro_station"):
        parts.append(f"метро {row['metro_station']}")
    wm = row.get("walk_min_metro")
    if wm is not None:
        parts.append(f"метро в {wm:.0f} мин пешком")
    wp = row.get("walk_min_park")
    if wp is not None:
        parts.append(f"парк в {wp:.0f} мин пешком")
    bd = row.get("bar_density_500m")
    if bd is not None:
        parts.append("баров в 500 м нет" if bd == 0 else f"баров рядом: {bd}")
    noise = row.get("noise_level")
    if noise:
        parts.append({"low": "тихо", "medium": "умеренно шумно"}.get(noise, "шумно"))
    if row.get("description"):
        parts.append(row["description"].strip())
    return ", ".join(parts)


def content_hash(text: str) -> str:
    return hashlib.sha1(text.encode("utf-8")).hexdigest()


def refresh_doc_text(conn, external_ids: list[str] | None = None) -> int:
    """Пересобирает doc_text. Без списка — по всей таблице (батч-пайплайн),
    со списком — только по указанным объявлениям (публикация из кабинета)."""
    from psycopg.rows import dict_row

    sql = "SELECT * FROM listings"
    params: tuple = ()
    if external_ids is not None:
        if not external_ids:
            return 0
        sql += " WHERE external_id = ANY(%s)"
        params = (list(external_ids),)
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute(sql + ";", params)
        rows = cur.fetchall()
    with conn.cursor() as cur:
        for r in rows:
            cur.execute("UPDATE listings SET doc_text=%s WHERE external_id=%s;",
                        (build_doc_text(r), r["external_id"]))
    conn.commit()
    return len(rows)
