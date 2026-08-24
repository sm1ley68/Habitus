"""Приём объявления из личного кабинета продавца в витрину.

Отдельный модуль, а не часть ingest/: объявление продавца не приходит обходом
источника и не попадает в raw_listings — эта таблица зеркалит снимок краулера,
а у объявления из кабинета никакого снимка нет.
"""
import psycopg

from habitus.clean.normalize import is_valid
from habitus.embed.document import refresh_doc_text
from habitus.embed.encode import embed_pending
from habitus.geo.enrich import enrich_ids
from habitus.online.schema import OwnerListingUpsertRequest


class OwnerListingInvalid(Exception):
    """Объявление не проходит те же пороги, что и объявление источника."""

    def __init__(self, field: str, message: str):
        super().__init__(message)
        self.field = field


_UPSERT_SQL = """
    INSERT INTO listings
      (external_id, source, price, area, kitchen_area, rooms, level, levels,
       geom, description, city, address, source_url, window_orientation,
       photos, owner_managed, is_active)
    VALUES
      (%(external_id)s, %(source)s, %(price)s, %(area)s, %(kitchen_area)s,
       %(rooms)s, %(level)s, %(levels)s,
       ST_SetSRID(ST_MakePoint(%(lng)s, %(lat)s), 4326), %(description)s,
       %(city)s, %(address)s, %(source_url)s, %(window_orientation)s,
       %(photos)s, true, true)
    ON CONFLICT (external_id) DO UPDATE SET
       price=EXCLUDED.price, area=EXCLUDED.area,
       kitchen_area=EXCLUDED.kitchen_area, rooms=EXCLUDED.rooms,
       level=EXCLUDED.level, levels=EXCLUDED.levels, geom=EXCLUDED.geom,
       description=EXCLUDED.description, city=EXCLUDED.city,
       address=EXCLUDED.address, source_url=EXCLUDED.source_url,
       window_orientation=EXCLUDED.window_orientation, photos=EXCLUDED.photos,
       owner_managed=true, is_active=true, updated_at=now();
"""


def _validate(req: OwnerListingUpsertRequest) -> None:
    row = {"price": req.price, "area": req.area,
           "lat": req.lat, "lon": req.lng, "city": req.city}
    if not (req.price and 1_000_000 <= req.price <= 3_000_000_000):
        raise OwnerListingInvalid("price", "Цена вне диапазона 1 млн — 3 млрд ₽")
    if not (req.area and 5 <= req.area <= 1000):
        raise OwnerListingInvalid("area", "Площадь вне диапазона 5—1000 м²")
    if not is_valid(row):
        raise OwnerListingInvalid("coordinates",
                                  "Координаты вне границ выбранного города")


def upsert_owner_listing(req: OwnerListingUpsertRequest,
                         conn: psycopg.Connection, model=None) -> bool:
    """Кладёт объявление в витрину, обогащает и индексирует его.

    Возвращает True, если объект получил эмбеддинг и, значит, находится
    семантическим поиском. Объект без вектора хуже отсутствующего: он лежит в
    базе и не находится, поэтому результат индексации возвращается наружу.
    """
    _validate(req)
    with conn.cursor() as cur:
        cur.execute(_UPSERT_SQL, {
            "external_id": req.external_id, "source": req.source,
            "price": req.price, "area": req.area, "kitchen_area": req.kitchen_area,
            "rooms": req.rooms, "level": req.level, "levels": req.levels,
            "lng": req.lng, "lat": req.lat, "description": req.description or None,
            "city": req.city, "address": req.address or None,
            "source_url": req.source_url or None,
            "window_orientation": req.window_orientation or None,
            "photos": req.photos or None,
        })
    conn.commit()

    enrich_ids(conn, [req.external_id])
    refresh_doc_text(conn, [req.external_id])
    embed_pending(conn, model=model, external_ids=[req.external_id])

    with conn.cursor() as cur:
        cur.execute("SELECT embedding IS NOT NULL FROM listings WHERE external_id=%s;",
                    (req.external_id,))
        row = cur.fetchone()
    return bool(row and row[0])


def withdraw_owner_listing(external_id: str, conn: psycopg.Connection) -> bool:
    """Снимает объявление с публикации. Строку не удаляет: повторная
    публикация должна оживить объект вместе с уже посчитанным эмбеддингом."""
    with conn.cursor() as cur:
        cur.execute("""UPDATE listings SET is_active=false, updated_at=now()
                       WHERE external_id=%s AND owner_managed;""", (external_id,))
        affected = cur.rowcount
    conn.commit()
    return affected > 0
