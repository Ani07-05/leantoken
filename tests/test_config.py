import os

from leantoken import config


def use_tmp(monkeypatch, tmp_path):
    monkeypatch.setattr(config, "GLOBAL_ENV", tmp_path / "home" / ".env")
    monkeypatch.setattr(config, "REAL_ENV", {})
    monkeypatch.chdir(tmp_path)


def test_set_unset_keeps_comments(monkeypatch, tmp_path):
    use_tmp(monkeypatch, tmp_path)
    config.set_value("BIFROST_VK", "sk-bf-123")
    config.set_value("NEW_KEY", "x")
    text = config.GLOBAL_ENV.read_text()
    assert "BIFROST_VK=sk-bf-123" in text and "NEW_KEY=x" in text and "# Bifrost gateway" in text
    config.set_value("NEW_KEY", None)
    assert "NEW_KEY" not in config.GLOBAL_ENV.read_text()


def test_local_env_overrides_global(monkeypatch, tmp_path):
    use_tmp(monkeypatch, tmp_path)
    config.set_value("LT_TEST_A", "global")
    config.set_value("LT_TEST_B", "global")
    (tmp_path / ".env").write_text("LT_TEST_A=local\n")
    monkeypatch.delenv("LT_TEST_A", raising=False)
    monkeypatch.delenv("LT_TEST_B", raising=False)
    config.load_all()
    assert os.environ["LT_TEST_A"] == "local" and os.environ["LT_TEST_B"] == "global"
    rows = {k: (v, src) for k, v, src in config.rows()}
    assert rows["LT_TEST_A"] == ("local", "./.env")
    monkeypatch.delenv("LT_TEST_A")
    monkeypatch.delenv("LT_TEST_B")


def test_secrets_masked():
    assert config.mask("BIFROST_VK", "sk-bf-1234567890abcd") == "sk-b…abcd"
    assert config.mask("BIFROST_URL", "http://localhost:8080") == "http://localhost:8080"


def test_settings_page_opens_without_key_and_saves(monkeypatch, tmp_path):
    import asyncio

    from leantoken import tui
    from leantoken.budget import Budget

    use_tmp(monkeypatch, tmp_path)
    monkeypatch.delenv("BIFROST_VK")
    app = tui.TUI(tmp_path, Budget(tmp_path / "b.json", 1.0), False, "groq/big", "groq/small")

    async def go():
        async with app.run_test(size=(100, 40)) as pilot:
            await pilot.pause(0.1)
            assert isinstance(app.screen, tui.Settings)
            assert app.focused.id == "f-BIFROST_VK"
            await pilot.press(*"sk-bf-new", "ctrl+s")
            await pilot.pause(0.1)
            assert not isinstance(app.screen, tui.Settings)

    asyncio.run(go())
    assert "BIFROST_VK=sk-bf-new" in config.GLOBAL_ENV.read_text()
    assert os.environ["BIFROST_VK"] == "sk-bf-new"
