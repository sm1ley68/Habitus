from habitus.config import settings

def test_settings_defaults():
    assert settings.embed_dim == 1024
    assert settings.embed_model == "BAAI/bge-m3"
    assert settings.poi_radius_m == 500
    assert settings.city_region_code == 3
    assert "postgresql://" in settings.db_dsn
