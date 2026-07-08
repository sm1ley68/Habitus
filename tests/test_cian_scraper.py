from pathlib import Path
from habitus.ingest.cian_scraper import parse_listing_html

FIX = (Path(__file__).parent / "fixtures" / "cian_page.html").read_text(encoding="utf-8")


def test_parse_listing_html():
    rows = parse_listing_html(FIX)
    assert len(rows) == 2
    r = rows[0]
    assert r["external_id"] == "cian_cian-1001"
    assert r["source"] == "cian"
    assert r["price"] == 12000000
    assert r["rooms"] == 2
    assert "школа рядом" in r["description"]
    assert abs(r["lat"] - 55.7558) < 1e-4


def test_card_without_id_is_skipped():
    html = ('<div data-testid="offer-card" data-price="5000000" data-area="30">'
            '<div class="descr">без id</div></div>'
            '<div data-testid="offer-card" data-price="6000000" data-area="35" '
            'data-id="cian-2002"><div class="descr">с id</div></div>')
    rows = parse_listing_html(html)
    assert len(rows) == 1
    assert rows[0]["external_id"] == "cian_cian-2002"
