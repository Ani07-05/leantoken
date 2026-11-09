"""Bifrost connection settings, Groq model discovery and the saved model choice."""

import json
import os
from pathlib import Path

import httpx

CONFIG = Path.home() / ".leantoken" / "config.json"
MODELS_CACHE = Path.home() / ".leantoken" / "models.json"
DEFAULT_SMALL = "groq/openai/gpt-oss-20b"
# speech and classifier models can't run the agent
NON_CHAT = ("whisper", "tts", "orpheus", "playai", "guard")


def bifrost_url() -> str:
    return os.environ.get("BIFROST_URL", "http://localhost:8080").rstrip("/")


def headers(private: bool = False) -> dict:
    var = "BIFROST_PRIVATE_VK" if private else "BIFROST_VK"
    vk = os.environ.get(var, "")
    if private and not vk:
        raise SystemExit("BIFROST_PRIVATE_VK is not set: private runs need the Ollama-only virtual key")
    return {"x-bf-vk": vk} if vk else {}


def groq_model(model: str) -> str:
    return model if model.startswith("groq/") else f"groq/{model}"


def list_models() -> list[str]:
    """Groq chat models from Bifrost (slow: Bifrost asks Groq); refreshes the local cache."""
    try:
        r = httpx.get(f"{bifrost_url()}/v1/models", headers=headers(), timeout=30)
    except httpx.ConnectError:
        raise SystemExit(f"cannot reach Bifrost at {bifrost_url()}: start it with `npx -y @maximhq/bifrost`")
    if r.is_error:
        try:
            msg = r.json()["error"]["message"]
        except (ValueError, KeyError, TypeError):
            msg = r.text[:200]
        raise SystemExit(f"Bifrost refused to list models ({r.status_code}): {msg}")
    ids = [m["id"] for m in r.json().get("data", [])]
    models = sorted(i for i in ids if i.startswith("groq/") and not any(x in i for x in NON_CHAT))
    if models:
        MODELS_CACHE.parent.mkdir(parents=True, exist_ok=True)
        MODELS_CACHE.write_text(json.dumps(models))
    return models


def cached_models() -> list[str]:
    """Last list Bifrost returned, so the picker opens instantly."""
    try:
        return json.loads(MODELS_CACHE.read_text())
    except (OSError, ValueError):
        return []


def _saved() -> dict:
    try:
        return json.loads(CONFIG.read_text())
    except (OSError, ValueError):
        return {}


def saved_model() -> str | None:
    return _saved().get("model")


def saved_small() -> str | None:
    """The first model; "" means no routing (large model only)."""
    return _saved().get("small")


def save_model(model: str | None = None, small: str | None = None) -> None:
    data = _saved()
    if model is not None:
        data["model"] = model
    if small is not None:
        data["small"] = small
    CONFIG.parent.mkdir(parents=True, exist_ok=True)
    CONFIG.write_text(json.dumps(data))


def _choose(models: list[str], prompt: str, allow_none: bool = False) -> str:
    for i, m in enumerate(models, 1):
        print(f"{i:>2}. {m}")
    if allow_none:
        print(" 0. none: use the large model only")
    while True:
        choice = input(prompt).strip()
        if allow_none and choice == "0":
            return ""
        if choice.isdigit() and 1 <= int(choice) <= len(models):
            return models[int(choice) - 1]
        print("invalid choice")


def pick_model() -> str:
    models = list_models()
    if not models:
        raise SystemExit("Bifrost lists no Groq models: add the Groq provider in the Bifrost UI first")
    model = _choose(models, "large model number: ")
    small = _choose(models, "first model number (tried before the large one, 0 for none): ", allow_none=True)
    save_model(model, small)
    print(f"saved {small + ' > ' if small else ''}{model} to {CONFIG}")
    return model


def resolve_model(flag: str | None) -> str:
    """--model flag > LEANTOKEN_MODEL > saved choice > interactive pick."""
    return flag or os.environ.get("LEANTOKEN_MODEL") or saved_model() or pick_model()


def resolve_small(flag: str | None) -> str:
    """--small flag > LEANTOKEN_SMALL_MODEL > saved choice > default."""
    if flag is not None:
        return flag
    small = os.environ.get("LEANTOKEN_SMALL_MODEL") or saved_small()
    return DEFAULT_SMALL if small is None else small
