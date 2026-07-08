import time
import requests
from bs4 import BeautifulSoup

BASE = "https://www.cian.ru/cat.php"


def _int(v):
    try:
        return int(float(v))
    except (ValueError, TypeError):
        return None


def parse_listing_html(html: str) -> list[dict]:
    soup = BeautifulSoup(html, "html.parser")
    rows = []
    for card in soup.select('[data-testid="offer-card"]'):
        descr_el = card.select_one(".descr")
        rows.append({
            "external_id": "cian_" + card.get("data-id", ""),
            "source": "cian",
            "price": _int(card.get("data-price")),
            "area": float(card["data-area"]) if card.get("data-area") else None,
            "kitchen_area": None,
            "rooms": _int(card.get("data-rooms")),
            "level": None, "levels": None,
            "building_type": None, "object_type": None,
            "lat": float(card["data-lat"]) if card.get("data-lat") else None,
            "lon": float(card["data-lon"]) if card.get("data-lon") else None,
            "description": descr_el.get_text(strip=True) if descr_el else None,
        })
    return rows


def scrape(pages: int, http_get=requests.get) -> list[dict]:
    all_rows = []
    for page in range(1, pages + 1):
        r = http_get(BASE, params={"deal_type": "sale", "region": 1, "p": page},
                     headers={"User-Agent": "Mozilla/5.0"}, timeout=30)
        r.raise_for_status()
        all_rows.extend(parse_listing_html(r.text))
        time.sleep(3.0)  # вежливая задержка
    return all_rows
