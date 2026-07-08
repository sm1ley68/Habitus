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
