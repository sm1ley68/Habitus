# habitus/db/init_db.py
import logging
from pathlib import Path
import psycopg

SCHEMA_PATH = Path(__file__).parent / "schema.sql"

log = logging.getLogger("habitus.db")

# HNSW по sparse-каналу живёт отдельно от schema.sql намеренно. pgvector
# отказывается индексировать sparsevec с >1000 ненулевых элементов, а такие
# строки могли попасть в БД до появления SPARSE_MAX_NNZ в embed/encode.py.
# Внутри schema.sql отказ оборвал бы применение ВСЕЙ схемы (это один execute) и
# уронил старт ML-сервиса; здесь он деградирует до предупреждения — sparse-канал
# продолжит работать, просто последовательным сканом, как и до индекса.
SPARSE_INDEX_SQL = """
CREATE INDEX IF NOT EXISTS listings_sparse_hnsw
    ON listings USING hnsw (sparse_embedding sparsevec_cosine_ops);
"""


def ensure_sparse_index(conn: psycopg.Connection) -> bool:
    """True — индекс есть или создан; False — не удалось, работаем без него."""
    try:
        with conn.transaction():
            conn.execute(SPARSE_INDEX_SQL)
        return True
    except psycopg.Error as exc:
        log.warning("HNSW по sparse_embedding не создан, sparse-канал пойдёт "
                    "seq-scan'ом: %s", exc)
        return False


def init_db(conn: psycopg.Connection) -> None:
    sql = SCHEMA_PATH.read_text(encoding="utf-8")
    with conn.cursor() as cur:
        cur.execute(sql)
    conn.commit()
    ensure_sparse_index(conn)
