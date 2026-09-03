from habitus.config import Settings, settings


def test_settings_defaults():
    assert settings.embed_dim == 1024
    assert settings.embed_model == "BAAI/bge-m3"
    assert settings.poi_radius_m == 500
    assert settings.city_region_code == 3
    assert "postgresql://" in settings.db_dsn


# Гейт «настроен ли ORS» раньше смотрел только на непустой ORS_API_KEY, и
# поднять свой инстанс было нельзя без фиктивного ключа: публичный ORS требует
# ключ, свой — ничего.
def test_ors_not_configured_without_key_on_public_endpoint():
    s = Settings(ors_api_key="", ors_base_url="https://api.openrouteservice.org")
    assert s.ors_configured is False


def test_ors_configured_by_key_on_public_endpoint():
    s = Settings(ors_api_key="k", ors_base_url="https://api.openrouteservice.org")
    assert s.ors_configured is True


def test_own_instance_needs_no_key():
    s = Settings(ors_api_key="", ors_base_url="http://ors:8080/ors")
    assert s.ors_configured is True


def test_trailing_slash_does_not_fake_an_own_instance():
    s = Settings(ors_api_key="", ors_base_url="https://api.openrouteservice.org/")
    assert s.ors_configured is False
