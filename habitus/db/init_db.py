# habitus/db/init_db.py
from pathlib import Path
import psycopg

SCHEMA_PATH = Path(__file__).parent / "schema.sql"

def init_db(conn: psycopg.Connection) -> None:
    sql = SCHEMA_PATH.read_text(encoding="utf-8")
    with conn.cursor() as cur:
        cur.execute(sql)
    conn.commit()
