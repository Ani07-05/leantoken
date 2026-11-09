"""Bifrost connection settings, Groq model discovery and the saved model choice."""

import json
import os
from pathlib import Path

import httpx

CONFIG = Path.home() / ".leantoken" / "config.json"
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
    r = httpx.get(f"{bifrost_url()}/v1/models", headers=headers(), timeout=30)
    r.raise_for_status()
    ids = [m["id"] for m in r.json().get("data", [])]
    return sorted(i for i in ids if i.startswith("groq/") and not any(x in i for x in NON_CHAT))


def saved_model() -> str | None:
    try:
        return json.loads(CONFIG.read_text()).get("model")
    except (OSError, ValueError):
        return None


def save_model(model: str) -> None:
    CONFIG.parent.mkdir(parents=True, exist_ok=True)
    CONFIG.write_text(json.dumps({"model": model}))


def pick_model() -> str:
    models = list_models()
    if not models:
        raise SystemExit("Bifrost lists no Groq models: add the Groq provider in the Bifrost UI first")
    for i, m in enumerate(models, 1):
        print(f"{i:>2}. {m}")
    while True:
        choice = input("pick a model number: ").strip()
        if choice.isdigit() and 1 <= int(choice) <= len(models):
            model = models[int(choice) - 1]
            save_model(model)
            print(f"saved {model} to {CONFIG}")
            return model
        print("invalid choice")


def resolve_model(flag: str | None) -> str:
    """--model flag > LEANTOKEN_MODEL > saved choice > interactive pick."""
    return flag or os.environ.get("LEANTOKEN_MODEL") or saved_model() or pick_model()
