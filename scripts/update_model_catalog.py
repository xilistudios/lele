#!/usr/bin/env python3
"""Regenerate catalog/ from models.dev.

Splits curated per-provider model files under catalog/providers/ and a
lightweight catalog/index.json used by lele clients for disk-cache downloads.

Usage:
  python3 scripts/update_model_catalog.py
  python3 scripts/update_model_catalog.py --source /path/to/models.dev.json
"""

from __future__ import annotations

import argparse
import json
import sys
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

MODELS_DEV_URL = "https://models.dev/api.json"
REPO_ROOT = Path(__file__).resolve().parents[1]
CATALOG_DIR = REPO_ROOT / "catalog"

# lele provider id -> models.dev provider id
MDEV_TO_LELE: dict[str, str] = {
    "openai": "openai",
    "anthropic": "anthropic",
    "openrouter": "openrouter",
    "groq": "groq",
    "deepseek": "deepseek",
    "gemini": "google",
    "zhipu": "zhipuai",
    "zai": "zai",
    "zai_coding_plan": "zai-coding-plan",
    "moonshot": "moonshotai-cn",
    "kimi_for_coding": "kimi-for-coding",
    "nvidia": "nvidia",
    "ollama_cloud": "ollama-cloud",
    "chutes": "chutes",
    "alibaba": "alibaba",
    "alibaba_coding_plan": "alibaba-coding-plan",
    "alibaba_token_plan": "alibaba-token-plan",
    "alibaba_token_plan_cn": "alibaba-token-plan-cn",
    "xai": "xai",
    "lmstudio": "lmstudio",
    "stepfun": "stepfun-ai-step-plan",
    "minimax": "minimax",
    "minimax_cn": "minimax-cn",
    "vercel": "vercel",
    "opencode": "opencode",
    "opencode_go": "opencode-go",
    "huggingface": "huggingface",
    "novita": "novita-ai",
    "xiaomi": "xiaomi",
    "tencent_tokenhub": "tencent-tokenhub",
    "arcee": "arcee",
    "gmi": "gmicloud",
    "cerebras": "cerebras",
    "together": "togetherai",
    "fireworks": "fireworks-ai",
    "mistral": "mistral",
    "siliconflow": "siliconflow",
    "perplexity": "perplexity-agent",
    "azure_foundry": "azure",
    "bedrock": "amazon-bedrock",
}

# Optional per-provider API base overrides (lele defaults win over models.dev).
API_BASE_OVERRIDES: dict[str, str] = {
    "openai": "https://api.openai.com/v1",
    "anthropic": "https://api.anthropic.com/v1",
    "openrouter": "https://openrouter.ai/api/v1",
    "groq": "https://api.groq.com/openai/v1",
    "deepseek": "https://api.deepseek.com/v1",
    "gemini": "https://generativelanguage.googleapis.com/v1beta",
    "zhipu": "https://open.bigmodel.cn/api/paas/v4",
    "zai": "https://api.z.ai/api/paas/v4",
    "zai_coding_plan": "https://api.z.ai/api/coding/paas/v4",
    "moonshot": "https://api.moonshot.cn/v1",
    "kimi_for_coding": "https://api.kimi.com/coding/v1",
    "nvidia": "https://integrate.api.nvidia.com/v1",
    "ollama_cloud": "https://ollama.com/v1",
    "chutes": "https://llm.chutes.ai/v1",
    "alibaba": "https://coding-intl.dashscope.aliyuncs.com/v1",
    "alibaba_coding_plan": "https://coding-intl.dashscope.aliyuncs.com/v1",
    "alibaba_token_plan": "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
    "alibaba_token_plan_cn": "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
    "xai": "https://api.x.ai/v1",
    "lmstudio": "http://127.0.0.1:1234/v1",
    "stepfun": "https://api.stepfun.ai/step_plan/v1",
    "minimax": "https://api.minimax.io/anthropic/v1",
    "minimax_cn": "https://api.minimaxi.com/anthropic/v1",
    "vercel": "https://ai-gateway.vercel.sh/v1",
    "opencode": "https://opencode.ai/zen/v1",
    "opencode_go": "https://opencode.ai/zen/go/v1",
    "huggingface": "https://router.huggingface.co/v1",
    "novita": "https://api.novita.ai/openai",
    "xiaomi": "https://api.xiaomimimo.com/v1",
    "tencent_tokenhub": "https://tokenhub.tencentmaas.com/v1",
    "arcee": "https://api.arcee.ai/api/v1",
    "gmi": "https://api.gmi-serving.com/v1",
    "cerebras": "https://api.cerebras.ai/v1",
    "together": "https://api.together.xyz/v1",
    "fireworks": "https://api.fireworks.ai/inference/v1",
    "mistral": "https://api.mistral.ai/v1",
    "siliconflow": "https://api.siliconflow.com/v1",
    "perplexity": "https://api.perplexity.ai/v1",
}

# Local/static providers not present on models.dev.
STATIC_PROVIDERS: dict[str, dict] = {
    "ollama": {
        "name": "Ollama (local)",
        "type": "openai",
        "api_base": "http://localhost:11434/v1",
        "models": [
            {"id": "llama3.2", "name": "Llama 3.2", "context_window": 128000, "max_output": 8192},
            {"id": "llama3.2-vision", "name": "Llama 3.2 Vision", "context_window": 128000, "max_output": 8192, "vision": True},
            {"id": "qwen2.5-coder", "name": "Qwen 2.5 Coder", "context_window": 32768, "max_output": 8192},
            {"id": "qwen2.5vl", "name": "Qwen 2.5 VL", "context_window": 32768, "max_output": 8192, "vision": True},
            {"id": "gemma3", "name": "Gemma 3", "context_window": 128000, "max_output": 8192, "vision": True},
            {"id": "deepseek-r1", "name": "DeepSeek R1", "context_window": 65536, "max_output": 8192, "thinking_levels": ["low", "medium", "high"], "reasoning": True},
            {"id": "phi4", "name": "Phi-4", "context_window": 16384, "max_output": 4096},
            {"id": "mistral-small3.1", "name": "Mistral Small 3.1", "context_window": 128000, "max_output": 8192, "vision": True},
        ],
    },
    "github_copilot": {
        "name": "GitHub Copilot",
        "type": "openai",
        "api_base": "https://api.githubcopilot.com/v1",
        "models": [
            {"id": "gpt-4o", "name": "GPT-4o (Copilot)", "context_window": 128000, "max_output": 16384, "vision": True},
            {"id": "gpt-4.1", "name": "GPT-4.1 (Copilot)", "context_window": 1000000, "max_output": 32768, "vision": True},
            {"id": "o4-mini", "name": "o4-mini (Copilot)", "context_window": 200000, "max_output": 100000, "vision": True, "thinking_levels": ["low", "medium", "high"], "reasoning": True},
            {"id": "claude-sonnet-4", "name": "Claude Sonnet 4 (Copilot)", "context_window": 200000, "max_output": 64000, "vision": True, "thinking_levels": ["low", "medium", "high"], "reasoning": True},
            {"id": "gemini-2.5-pro", "name": "Gemini 2.5 Pro (Copilot)", "context_window": 1000000, "max_output": 65536, "vision": True, "thinking_levels": ["low", "medium", "high"], "reasoning": True},
        ],
    },
    "nous": {
        "name": "Nous Portal",
        "type": "openai",
        "api_base": "https://inference-api.nousresearch.com/v1",
        "models": [
            {"id": "Hermes-4-405B", "name": "Hermes 4 405B", "context_window": 128000, "max_output": 8192, "thinking_levels": ["low", "medium", "high"], "reasoning": True},
            {"id": "Hermes-4-70B", "name": "Hermes 4 70B", "context_window": 128000, "max_output": 8192, "thinking_levels": ["low", "medium", "high"], "reasoning": True},
            {"id": "Hermes-3-Llama-3.1-405B", "name": "Hermes 3 405B", "context_window": 128000, "max_output": 8192},
            {"id": "Hermes-3-Llama-3.1-70B", "name": "Hermes 3 70B", "context_window": 128000, "max_output": 8192},
        ],
    },
    "vllm": {
        "name": "vLLM (local)",
        "type": "openai",
        "api_base": "http://localhost:8000/v1",
        "models": [],
    },
    "qwen_portal": {
        "name": "Qwen Portal",
        "type": "openai",
        "api_base": "https://portal.qwen.ai/v1",
        "models": [],
    },
    "shengsuanyun": {
        "name": "ShengsuanYun",
        "type": "openai",
        "api_base": "https://router.shengsuanyun.com/api/v1",
        "models": [],
    },
    "nanogpt": {
        "name": "NanoGPT",
        "type": "openai",
        "api_base": "https://nano-gpt.com/api/v1",
        "models": [],
    },
    "modelark": {
        "name": "ModelArk Coding Plan",
        "type": "openai",
        "api_base": "https://ark.ap-southeast.bytepluses.com/api/coding/v3",
        "models": [],
    },
}

# Cap large aggregator catalogs so files stay reasonable; full lists remain
# available via live /v1/models fetch.
MODEL_LIMITS: dict[str, int] = {
    "openrouter": 40,
    "vercel": 30,
    "opencode": 40,
    "opencode_go": 30,
    "huggingface": 40,
    "novita": 40,
    "nvidia": 40,
    "together": 30,
    "siliconflow": 30,
    "mistral": 25,
    "fireworks": 23,
    "alibaba": 25,
    "azure_foundry": 20,
    "bedrock": 20,
}


def thinking_levels(m: dict) -> list[str]:
    levels: list[str] = []
    for opt in m.get("reasoning_options") or []:
        if not isinstance(opt, dict) or opt.get("type") != "effort":
            continue
        for v in opt.get("values") or []:
            v = str(v).lower().strip()
            if v == "max":
                v = "high"
            if v in ("low", "medium", "high") and v not in levels:
                levels.append(v)
    if m.get("reasoning") and not levels:
        levels = ["low", "medium", "high"]
    return levels


def convert_model(m: dict) -> dict:
    limit = m.get("limit") or {}
    modalities = m.get("modalities") or {}
    inp = modalities.get("input") or []
    vision = bool(m.get("attachment")) or ("image" in inp)
    out = {
        "id": m.get("id") or "",
        "name": m.get("name") or m.get("id") or "",
        "context_window": int(limit.get("context") or 0),
        "max_output": int(limit.get("output") or 0),
        "vision": vision,
        "thinking_levels": thinking_levels(m),
        "reasoning": bool(m.get("reasoning")),
        "tool_call": bool(m.get("tool_call")),
    }
    # Drop empty optional fields for smaller diffs.
    if not out["name"]:
        del out["name"]
    if not out["thinking_levels"]:
        del out["thinking_levels"]
    for flag in ("vision", "reasoning", "tool_call"):
        if not out[flag]:
            del out[flag]
    if not out["context_window"]:
        del out["context_window"]
    if not out["max_output"]:
        del out["max_output"]
    return out


def score_model(m: dict) -> int:
    mid = (m.get("id") or "").lower()
    s = 0
    if m.get("status") == "deprecated":
        s -= 50
    if (m.get("limit") or {}).get("context"):
        s += 5
    if m.get("tool_call"):
        s += 8
    if m.get("reasoning"):
        s += 4
    if m.get("attachment") or "image" in ((m.get("modalities") or {}).get("input") or []):
        s += 3
    for kw, pts in [
        ("gpt-5", 20), ("gpt-4.1", 18), ("gpt-4o", 16), ("o4", 14), ("o3", 12),
        ("claude-sonnet-4", 20), ("claude-opus-4", 18),
        ("gemini-2.5", 18), ("gemini-2.0", 12),
        ("deepseek-v3", 16), ("deepseek-r1", 16), ("deepseek-chat", 14),
        ("qwen3", 14), ("qwen2.5", 10),
        ("glm-4.6", 14), ("glm-4.5", 12),
        ("kimi-k2", 14),
        ("grok-4", 16), ("grok-3", 12),
        ("llama-3.3", 12), ("llama-4", 14),
        ("mistral-large", 12), ("codestral", 10),
        ("nova-pro", 10),
        ("command-r", 10), ("command-a", 10),
        ("minimax-m", 10),
    ]:
        if kw in mid:
            s += pts
    return s


def load_models_dev(source: str | None) -> dict:
    if source:
        return json.loads(Path(source).read_text(encoding="utf-8"))
    req = urllib.request.Request(
        MODELS_DEV_URL,
        headers={"Accept": "application/json", "User-Agent": "lele-catalog-updater/1.0"},
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def select_models(mdev_id: str, src: dict, limit: int | None) -> list[dict]:
    p = src.get(mdev_id) or {}
    models = p.get("models") or {}
    items = list(models.values()) if isinstance(models, dict) else list(models)
    scored = []
    for m in items:
        if not m.get("id") or m.get("status") == "deprecated":
            continue
        scored.append((score_model(m), m.get("id") or "", convert_model(m)))
    scored.sort(key=lambda t: (-t[0], t[1]))
    out = [c for _, _, c in scored]
    if limit and len(out) > limit:
        out = out[:limit]
    return out


def build_catalog(src: dict) -> dict:
    providers_dir = CATALOG_DIR / "providers"
    providers_dir.mkdir(parents=True, exist_ok=True)

    # Remove stale provider files so deleted providers disappear from the PR.
    for old in providers_dir.glob("*.json"):
        old.unlink()

    index = {
        "version": 1,
        "updated_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "source": "models.dev",
        "providers": {},
    }

    all_ids = set(MDEV_TO_LELE) | set(STATIC_PROVIDERS)
    for pid in sorted(all_ids):
        if pid in STATIC_PROVIDERS:
            static = STATIC_PROVIDERS[pid]
            payload = {
                "id": pid,
                "name": static["name"],
                "type": static.get("type") or "openai",
                "api_base": static.get("api_base") or API_BASE_OVERRIDES.get(pid, ""),
                "models": static.get("models") or [],
            }
        else:
            mdev_id = MDEV_TO_LELE[pid]
            mdev = src.get(mdev_id) or {}
            models = select_models(mdev_id, src, MODEL_LIMITS.get(pid))
            payload = {
                "id": pid,
                "name": mdev.get("name") or pid,
                "type": "anthropic" if pid in ("minimax", "minimax_cn", "anthropic") else "openai",
                "api_base": API_BASE_OVERRIDES.get(pid) or mdev.get("api") or "",
                "models": models,
            }

        fname = f"{pid}.json"
        (providers_dir / fname).write_text(
            json.dumps(payload, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
        )
        index["providers"][pid] = {
            "id": pid,
            "name": payload["name"],
            "type": payload["type"],
            "api_base": payload["api_base"],
            "file": f"providers/{fname}",
            "model_count": len(payload["models"]),
        }

    return index


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--source", help="Local models.dev api.json path (skip download)")
    args = ap.parse_args()

    print(f"Loading models.dev from {args.source or MODELS_DEV_URL}…")
    src = load_models_dev(args.source)
    index = build_catalog(src)

    (CATALOG_DIR / "index.json").write_text(
        json.dumps(index, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )

    n_models = sum(e["model_count"] for e in index["providers"].values())
    print(f"Wrote {len(index['providers'])} providers / {n_models} models → {CATALOG_DIR}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
