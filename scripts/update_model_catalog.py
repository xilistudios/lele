#!/usr/bin/env python3
"""Regenerate catalog/ from models.dev.

Splits curated per-provider model files under catalog/providers/ and a
lightweight catalog/index.json used by lele clients for disk-cache downloads.

Usage:
  python3 scripts/update_model_catalog.py
  python3 scripts/update_model_catalog.py --source /path/to/models.dev.json
  python3 scripts/update_model_catalog.py --providers opencode opencode_go
  python3 scripts/update_model_catalog.py --source snapshot.json --output-dir /tmp/catalog

Without --source the current models.dev/api.json is downloaded, so an offline run
needs a previously saved snapshot (see --source). The index `updated_at` only
moves when the generated content changes, which keeps reruns byte-identical.
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

# Issue #405: a live probe of POST https://opencode.ai/zen/go/v1/chat/completions
# (2026-10-22, every lele header, streaming and non-streaming) recorded which ids
# the gateway refuses over chat/completions. models.dev has no transport field, so
# this curated map is the source of truth for the protocol marker. Values name the
# wire protocol the id is served over; "" (absent) means chat/completions.
#   gpt-5.6-luna, grok-4.7  -> 400 ModelProtocolUnsupported (issue #405 report)
#   the remaining ids       -> the same 400, measured by the #403 coverage sweep
# models.dev's per-model `provider.npm` agrees on the family for several of them,
# but it also tags chat/completions-capable ids (hy4, kimi-k2.7-code) the same
# way, so it cannot replace this map.
PROTOCOL_MARKERS: dict[str, dict[str, str]] = {
    "opencode_go": {
        "gpt-5.6-luna": "responses",
        "gpt-6-luna": "responses",
        "grok-4.6": "responses",
        "grok-4.7": "responses",
        "muse-spark-1.2-contributor": "responses",
        "muse-spark-1.3-contributor": "responses",
        "claude-haiku-5-5": "anthropic_messages",
        "minimax-m2.7": "anthropic_messages",
        "gemini-4-argon-contributor": "gemini",
    },
}

# Fail-safe hiding policy, not a measurement (issue #405). A free-tier id on this
# gateway is a trial of a paid model and the two measured ones answer 400
# ModelDeprecated / 402 "Endpoint is unavailable", so the whole `-free` family is
# hidden: a suffix rule can only hide an id, never offer one the endpoint would
# reject, and the weekly run cannot probe the gateway to tell the families apart.
# Known false positive: `beam-free` was measured answering 200 and has no paid
# twin, so it is not offered by the picker either — it still works if typed, and
# no config validates against the catalog. Drop the suffix from this list to ship
# free ids again, but then re-measure the family first: an unmeasured free id
# that appears in a future snapshot (`hungerg-free` was one) would be OFFERED.
EXCLUDED_ID_SUFFIXES: dict[str, list[str]] = {
    "opencode_go": ["-free"],
}

# Ids measured live on the gateway that models.dev's mirror does not list yet
# (issue #405, 2026-10-22). They lead the model list so the picker's default entry
# is a verified id. They bypass the deprecation and the EXCLUDED_ID_SUFFIXES
# filters — the gateway is authoritative for what it serves — but they DO consume
# MODEL_LIMITS slots, which is why that cap has to cover pins plus the scored
# tail. A pin the source cannot resolve aborts the run instead of shipping a
# catalog whose leading entry silently stopped being verified.
PINNED_MODEL_IDS: dict[str, list[str]] = {
    "opencode_go": [
        "glm-5.1",
        "grok-4.6",
        "kimi-k2.6",
        "minimax-m2.5",
        "minimax-m2.7",
        "minimax-m3.1-flash",
        "omen-alpha",
        "qwen3.6-plus",
        "qwen3.7-max",
    ],
}

# Resolution order for a pinned id's metadata: the provider itself, then these
# siblings (the same models mirrored by a related provider), then
# CURATED_MODEL_RECORDS, and only then any other provider in the snapshot. The
# list is a PRIORITY HINT, not a boundary: `qwen3.7-max`, for example, is not in
# `opencode` either and resolves from `302ai`, so its limits are another vendor's
# numbers rather than a measurement on this gateway. Such a fallback is reported
# on stderr so a weekly run leaves a trace of it.
MODEL_METADATA_SIBLINGS: dict[str, list[str]] = {
    "opencode_go": ["opencode"],
}

# Pinned ids absent from models.dev entirely, recorded from the gateway listing.
# `omen-alpha` is measured working on chat/completions but no source publishes
# its limits, so the record stays name-only on purpose: the entry ships with
# `context_window`/`max_output` unset instead of borrowing another vendor's
# numbers. The wizard then falls back to catalog.DefaultsFor's documented
# unknown-model defaults (128000/8192) and the picker labels the id as having no
# published limits. Re-measure before filling these in.
CURATED_MODEL_RECORDS: dict[str, list[dict]] = {
    "opencode_go": [
        {"id": "omen-alpha", "name": "Omen Alpha"},
    ]
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
# available via live /v1/models fetch. The cap counts pinned ids too, so it has to
# leave room for the scored tail: `opencode_go` pins 9 ids, and a cap of 30 used to
# push 5 ids measured as working (`deepseek-v4-pro`, `glm-5.3`, `hy3`,
# `hy4-preview`, `step-5-preview`) off the end without a word. Whenever the cap
# does trim a scored model the run now reports how many it dropped.
MODEL_LIMITS: dict[str, int] = {
    "openrouter": 40,
    "vercel": 30,
    "opencode": 40,
    "opencode_go": 35,
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


def mdev_models(src: dict, mdev_id: str) -> dict[str, dict]:
    raw = (src.get(mdev_id) or {}).get("models") or {}
    items = list(raw.values()) if isinstance(raw, dict) else list(raw)
    return {(m.get("id") or ""): m for m in items}


def resolve_pinned_record(src: dict, provider_id: str, model_id: str) -> dict | None:
    own_and_siblings = [provider_id, *MODEL_METADATA_SIBLINGS.get(provider_id, [])]
    for lele_id in own_and_siblings:
        mdev_id = MDEV_TO_LELE.get(lele_id)
        if not mdev_id:
            continue
        record = mdev_models(src, mdev_id).get(model_id)
        if record:
            return record
    for record in CURATED_MODEL_RECORDS.get(provider_id) or []:
        if (record.get("id") or "") == model_id:
            return record
    for mdev_id in sorted(src):
        record = mdev_models(src, mdev_id).get(model_id)
        if record:
            print(
                f"note: metadata for pinned id {model_id!r} of {provider_id!r} comes from "
                f"unrelated provider {mdev_id!r}: cross-vendor limits, not a measurement",
                file=sys.stderr,
            )
            return record
    return None


def is_excluded(provider_id: str, model_id: str) -> bool:
    lowered = model_id.lower()
    return any(lowered.endswith(s) for s in EXCLUDED_ID_SUFFIXES.get(provider_id) or [])


def mark_protocol(model: dict, provider_id: str) -> dict:
    protocol = (PROTOCOL_MARKERS.get(provider_id) or {}).get(model.get("id") or "")
    if not protocol:
        return model
    marked = {"id": model.get("id") or "", "protocol": protocol}
    marked.update({k: v for k, v in model.items() if k != "id"})
    return marked


def pinned_models(provider_id: str, src: dict) -> list[dict]:
    out: list[dict] = []
    for model_id in PINNED_MODEL_IDS.get(provider_id) or []:
        record = resolve_pinned_record(src, provider_id, model_id)
        if record is None:
            raise SystemExit(
                f"pinned id {model_id!r} of provider {provider_id!r} is missing from the source: "
                f"refusing to ship a catalog whose leading entry is no longer verified "
                f"(re-measure the gateway and update PINNED_MODEL_IDS or CURATED_MODEL_RECORDS)"
            )
        out.append(convert_model(record))
    return out


def select_models(provider_id: str, mdev_id: str, src: dict, limit: int | None) -> list[dict]:
    pinned = pinned_models(provider_id, src)
    pinned_ids = {m["id"] for m in pinned}
    items = mdev_models(src, mdev_id).values()
    scored = []
    for m in items:
        model_id = m.get("id") or ""
        if not model_id or model_id in pinned_ids:
            continue
        if m.get("status") == "deprecated" or is_excluded(provider_id, model_id):
            continue
        scored.append((score_model(m), model_id, convert_model(m)))
    scored.sort(key=lambda t: (-t[0], t[1]))
    out = pinned + [converted for _, _, converted in scored]
    if limit and len(out) > limit:
        dropped = [m["id"] for m in out[limit:]]
        preview = ", ".join(dropped[:5]) + (", …" if len(dropped) > 5 else "")
        print(
            f"note: {provider_id} cap {limit} drops {len(dropped)} live id(s): {preview}",
            file=sys.stderr,
        )
        out = out[:limit]
    return [mark_protocol(m, provider_id) for m in out]


def build_provider_payload(provider_id: str, src: dict) -> dict:
    if provider_id in STATIC_PROVIDERS:
        static = STATIC_PROVIDERS[provider_id]
        return {
            "id": provider_id,
            "name": static["name"],
            "type": static.get("type") or "openai",
            "api_base": static.get("api_base") or API_BASE_OVERRIDES.get(provider_id, ""),
            "models": [mark_protocol(m, provider_id) for m in static.get("models") or []],
        }
    mdev_id = MDEV_TO_LELE[provider_id]
    mdev = src.get(mdev_id) or {}
    return {
        "id": provider_id,
        "name": mdev.get("name") or provider_id,
        "type": "anthropic" if provider_id in ("minimax", "minimax_cn", "anthropic") else "openai",
        "api_base": API_BASE_OVERRIDES.get(provider_id) or mdev.get("api") or "",
        "models": select_models(provider_id, mdev_id, src, MODEL_LIMITS.get(provider_id)),
    }


def index_entry(provider_id: str, payload: dict) -> dict:
    return {
        "id": provider_id,
        "name": payload["name"],
        "type": payload["type"],
        "api_base": payload["api_base"],
        "file": f"providers/{provider_id}.json",
        "model_count": len(payload["models"]),
    }


def dump_json(payload: dict) -> str:
    return json.dumps(payload, indent=2, ensure_ascii=False) + "\n"


def read_json(path: Path) -> dict:
    if not path.exists():
        return {}
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SystemExit(
            f"cannot read {path}: {exc} — refusing to rewrite it, that would drop every "
            f"provider a --providers run does not name"
        )
    if not isinstance(payload, dict):
        raise SystemExit(f"cannot use {path}: expected a JSON object, got {type(payload).__name__}")
    return payload


def write_provider(providers_dir: Path, provider_id: str, payload: dict) -> bool:
    path = providers_dir / f"{provider_id}.json"
    text = dump_json(payload)
    if path.exists() and path.read_text(encoding="utf-8") == text:
        return False
    path.write_text(text, encoding="utf-8")
    return True


def prune_providers(providers_dir: Path, keep: set[str]) -> bool:
    removed = False
    for old in providers_dir.glob("*.json"):
        if old.stem not in keep:
            old.unlink()
            removed = True
    return removed


def build_catalog(src: dict, catalog_dir: Path, only: list[str] | None) -> dict:
    providers_dir = catalog_dir / "providers"
    providers_dir.mkdir(parents=True, exist_ok=True)
    index_path = catalog_dir / "index.json"

    all_ids = sorted(set(MDEV_TO_LELE) | set(STATIC_PROVIDERS))
    unknown = sorted(set(only or []) - set(all_ids))
    if unknown:
        raise SystemExit(f"unknown provider id(s): {', '.join(unknown)}")
    targets = [pid for pid in all_ids if not only or pid in only]

    previous_index = read_json(index_path)
    previous_entries = previous_index.get("providers") or {}
    entries: dict[str, dict] = dict(previous_entries) if only else {}
    changed = False

    for pid in targets:
        payload = build_provider_payload(pid, src)
        changed = write_provider(providers_dir, pid, payload) or changed
        entries[pid] = index_entry(pid, payload)

    if not only:
        changed = prune_providers(providers_dir, set(entries)) or changed
        entries = {pid: entries[pid] for pid in sorted(entries)}

    if entries != previous_entries:
        changed = True

    updated_at = previous_index.get("updated_at") or ""
    if changed or not updated_at:
        updated_at = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

    index = {
        "version": previous_index.get("version", 1),
        "updated_at": updated_at,
        "source": "models.dev",
        "providers": entries,
    }
    index_path.write_text(dump_json(index), encoding="utf-8")
    return index


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--source", help="Local models.dev api.json path (skip download)")
    ap.add_argument("--output-dir", help="Write the catalog here instead of ./catalog")
    ap.add_argument(
        "--providers",
        nargs="+",
        metavar="ID",
        help="Regenerate only these provider ids and leave the rest of the catalog untouched",
    )
    args = ap.parse_args()

    print(f"Loading models.dev from {args.source or MODELS_DEV_URL}…")
    src = load_models_dev(args.source)
    catalog_dir = Path(args.output_dir) if args.output_dir else CATALOG_DIR
    index = build_catalog(src, catalog_dir, args.providers)

    total_models = sum(e["model_count"] for e in index["providers"].values())
    print(
        f"Wrote {len(index['providers'])} providers / {total_models} models → {catalog_dir} "
        f"(updated_at {index['updated_at']})"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
