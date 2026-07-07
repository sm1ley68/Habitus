# habitus/ingest/kaggle_loader.py
import csv
import hashlib
from pathlib import Path
import psycopg
from habitus.config import settings

def _external_id(row: dict) -> str:
    key = f"{row['geo_lat']}|{row['geo_lon']}|{row['area']}|{row['price']}|{row['date']}"
    return "kaggle_" + hashlib.sha1(key.encode()).hexdigest()[:16]

def _to_int(v):
    try: return int(float(v))
    except (ValueError, TypeError): return None

def _to_float(v):
    try: return float(v)
    except (ValueError, TypeError): return None

def parse_csv(path: Path) -> list[dict]:
    out = []
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            if _to_int(row.get("region")) != settings.city_region_code:
                continue
            out.append({
                "external_id": _external_id(row),
                "source": "kaggle",
                "price": _to_int(row.get("price")),
                "area": _to_float(row.get("area")),
                "kitchen_area": _to_float(row.get("kitchen_area")),
                "rooms": _to_int(row.get("rooms")),
                "level": _to_int(row.get("level")),
                "levels": _to_int(row.get("levels")),
                "building_type": _to_int(row.get("building_type")),
                "object_type": _to_int(row.get("object_type")),
                "lat": _to_float(row.get("geo_lat")),
                "lon": _to_float(row.get("geo_lon")),
                "description": None,
            })
    return out

def load_to_raw(rows: list[dict], conn: psycopg.Connection) -> int:
    cols = ["external_id","source","price","area","kitchen_area","rooms",
            "level","levels","building_type","object_type","lat","lon","description"]
    sql = f"""
        INSERT INTO raw_listings ({",".join(cols)})
        VALUES ({",".join("%("+c+")s" for c in cols)})
        ON CONFLICT (external_id) DO UPDATE SET
            price=EXCLUDED.price, area=EXCLUDED.area, description=EXCLUDED.description,
            ingested_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, rows)
    conn.commit()
    return len(rows)

def download_dataset() -> Path:
    """Скачивает mrdaniilak/russia-real-estate-20182021 в data_dir. Требует KAGGLE_KEY."""
    import kaggle
    dest = settings.data_dir / "kaggle"
    dest.mkdir(parents=True, exist_ok=True)
    kaggle.api.dataset_download_files(
        "mrdaniilak/russia-real-estate-20182021", path=str(dest), unzip=True)
    csvs = list(dest.glob("*.csv"))
    if not csvs:
        raise FileNotFoundError("CSV не найден после распаковки Kaggle-датасета")
    return max(csvs, key=lambda p: p.stat().st_size)
