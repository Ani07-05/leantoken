"""Per-task footprint: tokens, cost, latency, energy (kWh), carbon (gCO2e).

Energy follows the EcoLogits LLM inference method (ecologits.ai/latest/methodology/llm_inference,
constants from mlco2/ecologits impacts/llm.py), simplified:
  E = PUE * [ dT/3600 * 1.2kW * GPUs/8 / B  +  GPUs * T_out * (a*e^(b*B)*P_active + c) / 1000 ]
Boundary: EcoLogits models H100 servers, not Groq LPUs, and ignores input tokens for GPU energy;
Groq is not an EcoLogits provider. Treat the numbers as comparable estimates, not measurements.
"""

import json
import math
import os
import re
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path

from .budget import price

A, B_EXP, C = 1.1665273170451914e-06, -0.011205921025579175, 4.052928146734005e-05
BATCH = 64
SERVER_KW, GPUS_PER_SERVER, GPU_GB = 1.2, 8, 80
PUE = 1.2  # Groq not listed by EcoLogits; OpenAI's US value used
# gCO2e/kWh, Our World in Data 2023 (carbon intensity of electricity); override with LEANTOKEN_GRID_G
GRID_G_PER_KWH = {"IN": 713, "US": 369}
DEFAULT_GRID = "US"
# Groq data-center region (X-Groq-Region response header) -> grid
REGION_GRID = {"bom": "IN"}

# (total, active) params in billions; MoE active counts from the model cards
PARAMS = {
    "openai/gpt-oss-120b": (117, 5.1),
    "openai/gpt-oss-20b": (21, 3.6),
}


def params(model: str) -> tuple[float, float]:
    m = model.removeprefix("groq/")
    if m in PARAMS:
        return PARAMS[m]
    sizes = re.findall(r"(\d+(?:\.\d+)?)b\b", m.lower())
    p = float(sizes[-1]) if sizes else 70.0  # unknown size: assume a 70B dense model
    return p, p


def gpus(total_b: float) -> int:
    mem_gb = 1.2 * total_b * 16 / 8
    return 2 ** math.ceil(math.log2(max(1, math.ceil(mem_gb / GPU_GB))))


def energy_kwh(model: str, out_tokens: int, latency_s: float) -> float:
    total, active = params(model)
    n = gpus(total)
    gpu = out_tokens * (A * math.exp(B_EXP * BATCH) * active + C) / 1000
    server = latency_s / 3600 * SERVER_KW * n / GPUS_PER_SERVER / BATCH
    return PUE * (server + n * gpu)


def grid(region: str = "") -> float:
    if override := os.environ.get("LEANTOKEN_GRID_G"):
        return float(override)
    return GRID_G_PER_KWH[REGION_GRID.get(region, DEFAULT_GRID)]


@dataclass
class Call:
    model: str
    prompt: int
    completion: int
    latency_s: float
    cost: float
    kwh: float
    region: str = ""
    gco2e: float = 0.0
    local: bool = False


@dataclass
class Footprint:
    task: str
    calls: list = field(default_factory=list)
    escalations: list = field(default_factory=list)
    fallbacks: int = 0
    rate_limit_wait_s: float = 0.0
    saved_prompt: float = 0.0  # prompt tokens a naive agent would have sent on top of ours
    trace: list = field(default_factory=list)
    started: float = field(default_factory=time.time)

    def add(self, model: str, prompt: int, completion: int, latency_s: float, local: bool = False, region: str = "") -> Call:
        pin, pout = (0.0, 0.0) if local else price(model)
        # the EcoLogits formula models cloud GPUs, so it says nothing about a local Ollama run
        kwh = 0.0 if local else energy_kwh(model, completion, latency_s)
        c = Call(model, prompt, completion, round(latency_s, 3), (prompt * pin + completion * pout) / 1e6,
                 kwh, region, kwh * grid(region), local)
        self.calls.append(c)
        return c

    def totals(self) -> dict:
        kwh = sum(c.kwh for c in self.calls)
        by_model = {}
        for c in self.calls:
            m = by_model.setdefault(c.model, {"calls": 0, "in": 0, "out": 0, "cost": 0.0, "kwh": 0.0})
            m["calls"] += 1
            m["in"] += c.prompt
            m["out"] += c.completion
            m["cost"] += c.cost
            m["kwh"] += c.kwh
        used = sum(c.prompt + c.completion for c in self.calls)
        saved = round(self.saved_prompt)
        gco2e = sum(c.gco2e for c in self.calls)
        # the energy model ignores input tokens, so the saving is scaled from tokens: energy per token stays constant
        scale = saved / used if used else 0.0
        return {
            "saved_kwh": kwh * scale,
            "saved_gco2e": gco2e * scale,
            "saved_prompt": saved,
            "naive_total": used + saved,
            "saved_pct": round(100 * saved / (used + saved)) if used + saved else 0,
            "calls": len(self.calls),
            "in": sum(c.prompt for c in self.calls),
            "out": sum(c.completion for c in self.calls),
            "cost_usd": sum(c.cost for c in self.calls),
            "latency_s": round(sum(c.latency_s for c in self.calls), 2),
            "kwh": kwh,
            "gco2e": gco2e,
            "regions": sorted({c.region for c in self.calls if c.region}),
            "by_model": by_model,
        }

    def report(self, result: str, verified: str) -> str:
        t = self.totals()
        lines = [
            "",
            "-- footprint " + "-" * 47,
            f"result     {result[:70]}",
            f"verified   {verified}",
            f"calls      {t['calls']}  (fallbacks {self.fallbacks}, escalations {len(self.escalations)})",
            f"tokens     {t['in'] + t['out']:,}  (in {t['in']:,} / out {t['out']:,})",
            f"saved      ~{t['saved_prompt']:,} tokens ({t['saved_pct']}% fewer than a naive agent; estimate, see README)",
            f"cost       ${t['cost_usd']:.5f}  (price table; Bifrost logs are authoritative)",
            f"latency    {t['latency_s']}s model time" + (f", {self.rate_limit_wait_s:.0f}s rate-limit wait" if self.rate_limit_wait_s else ""),
            *self._footprint_lines(t),
        ]
        for m, v in t["by_model"].items():
            lines.append(f"  {m:<28} {v['calls']} calls  {v['in'] + v['out']:>7,} tok  ${v['cost']:.5f}  {v['kwh'] * 1000:.4f} Wh")
        for e in self.escalations:
            lines.append(f"  escalated at step {e['step']}: {e['reason']}")
        return "\n".join(lines)

    def _footprint_lines(self, t: dict) -> list[str]:
        if self.calls and all(c.local for c in self.calls):
            return ["energy     n/a  (local model, not estimated)", "carbon     n/a  (local model, not estimated)"]
        return [f"energy     {t['kwh'] * 1000:.4f} Wh  (EcoLogits estimate, PUE {PUE})",
                f"carbon     {t['gco2e'] * 1000:.3f} mgCO2e  (Groq region {','.join(t['regions']) or '?'}, grid {self._grid_label(t)})"]

    def _grid_label(self, t: dict) -> str:
        return ", ".join(f"{grid(r):.0f}" for r in (t["regions"] or [""])) + " g/kWh"

    def save(self, dirpath: Path, result: str, verified: str) -> Path:
        dirpath.mkdir(parents=True, exist_ok=True)
        path = dirpath / time.strftime("%Y%m%d-%H%M%S.json", time.localtime(self.started))
        data = {"task": self.task, "result": result, "verified": verified, "pue": PUE, "rate_limit_wait_s": self.rate_limit_wait_s,
                "totals": self.totals(), "escalations": self.escalations, "fallbacks": self.fallbacks,
                "calls": [asdict(c) for c in self.calls], "trace": self.trace}
        path.write_text(json.dumps(data, indent=1))
        return path
