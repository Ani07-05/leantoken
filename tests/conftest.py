import pytest

from leantoken import config, gateway


@pytest.fixture(autouse=True)
def isolated_settings(monkeypatch, tmp_path_factory):
    """Tests never touch the real ~/.leantoken files, and the TUI skips first-run settings."""
    home = tmp_path_factory.mktemp("home")
    monkeypatch.setattr(config, "GLOBAL_ENV", home / ".env")
    monkeypatch.setattr(config, "REAL_ENV", {})
    monkeypatch.setattr(gateway, "CONFIG", home / "config.json")
    monkeypatch.setattr(gateway, "MODELS_CACHE", home / "models.json")
    monkeypatch.setenv("BIFROST_VK", "sk-bf-test")
    monkeypatch.delenv("LEANTOKEN_SMALL_MODEL", raising=False)
