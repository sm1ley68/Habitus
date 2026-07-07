# habitus/db/connection.py
import psycopg
from habitus.config import settings

def get_conn() -> psycopg.Connection:
    return psycopg.connect(settings.db_dsn)
