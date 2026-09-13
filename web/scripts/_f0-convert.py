#!/usr/bin/env python3
"""
_f0-convert.py — Deterministic conversion for T0.6 (F0).

Converts CSS custom properties from hex/rgb/rgba to space-separated RGB triplets
so that Tailwind's <alpha-value> placeholder can work with opacity modifiers.

ALL --color-* vars are converted (including those with baked-in alpha).
rgba(r,g,b,a) -> 'r g b / a' (space-separated with /).

Applies to: web/src/styles/index.css and web/tailwind.config.js.

Usage:
    cd ~/lele-wt-webui/web
    python3 scripts/_f0-convert.py
"""

import re
import sys
from pathlib import Path

WEB = Path(__file__).resolve().parent.parent
CSS_PATH = WEB / "src" / "styles" / "index.css"
TWCFG_PATH = WEB / "tailwind.config.js"

# CSS color properties that need rgb() wrapping when referencing triplet vars.
COLOR_PROPS = (
    "background", "background-color", "color", "border-color",
    "outline-color", "text-decoration-color",
    "fill", "stroke", "caret-color", "accent-color", "column-rule-color",
)


# ─── Color conversion helpers ─────────────────────────────────────────────

def hex_to_triplet(hex_str: str) -> str:
    """#rgb / #rrggbb -> 'R G B'"""
    h = hex_str.lstrip("#")
    if len(h) == 3:
        h = "".join(c * 2 for c in h)
    return f"{int(h[0:2], 16)} {int(h[2:4], 16)} {int(h[4:6], 16)}"


def rgb_to_triplet(rgb_str: str) -> str:
    """rgb(r, g, b) -> 'R G B'"""
    nums = re.findall(r"[\d.]+", rgb_str)
    return f"{int(float(nums[0]))} {int(float(nums[1]))} {int(float(nums[2]))}"


def rgba_to_space_rgba(rgba_str: str) -> str:
    """rgba(r, g, b, a) -> 'R G B / A' (space-separated with /)"""
    nums = re.findall(r"[\d.]+", rgba_str)
    r, g, b = int(float(nums[0])), int(float(nums[1])), int(float(nums[2]))
    a = float(nums[3])
    return f"{r} {g} {b} / {a}"


def convert_color_value(val: str) -> str:
    """Convert a color value to space-separated triplet."""
    v = val.strip()
    if v.startswith("#"):
        return hex_to_triplet(v)
    elif v.startswith("rgba("):
        return rgba_to_space_rgba(v)
    elif v.startswith("rgb("):
        return rgb_to_triplet(v)
    return v


# ─── Step 1: index.css ───────────────────────────────────────────────────

def convert_index_css():
    css = CSS_PATH.read_text()

    # 1a) Replace ALL --color-X: <value>; declarations with triplet values.
    #     Each occurrence is converted independently (dark/light have different values).
    def replace_var_decl(m):
        name = m.group(1)
        val = m.group(2).strip()
        new_val = convert_color_value(val)
        return f"{name}: {new_val};"

    css = re.sub(
        r"(--color-[\w-]+)\s*:\s*([^;]+);",
        replace_var_decl,
        css,
    )

    # 1b) Convert body fallback: #1c1c1e -> 28 28 30
    css = css.replace(
        "var(--color-bg-primary, #1c1c1e)",
        "var(--color-bg-primary, 28 28 30)",
    )

    # 1c) Wrap var(--color-X...) in rgb() for CSS color property values.
    #     Handles both var(--color-X) and var(--color-X, fallback).
    lines = css.split("\n")
    new_lines = []

    for line in lines:
        # Skip custom property declarations (--color-X: ...)
        if re.match(r"\s*--color-", line):
            new_lines.append(line)
            continue

        # Skip lines without var(--color-...)
        if "var(--color-" not in line:
            new_lines.append(line)
            continue

        # Skip if already wrapped: rgb(var(--color-...))
        if "rgb(var(--color-" in line:
            new_lines.append(line)
            continue

        # Determine if this line's CSS property is a color property
        prop_match = re.match(r"\s*([a-z][\w-]*)\s*:", line)
        if not prop_match:
            new_lines.append(line)
            continue

        prop = prop_match.group(1)

        # Check if property accepts color values
        is_color_prop = prop in COLOR_PROPS
        # border shorthand: "border: 1px solid var(--color-X)"
        if prop == "border":
            is_color_prop = True
        # outline shorthand: "outline: 2px solid var(--color-X)"
        if prop == "outline":
            is_color_prop = True

        if not is_color_prop:
            new_lines.append(line)
            continue

        # Wrap each var(--color-X...) with rgb()
        # Match var(--color-X) AND var(--color-X, fallback)
        def repl_var(m):
            return f"rgb({m.group(0)})"

        line = re.sub(r"var\((--color-[\w-]+)(?:,[^)]+)?\)", repl_var, line)
        new_lines.append(line)

    css = "\n".join(new_lines)

    CSS_PATH.write_text(css)


# ─── Step 2: tailwind.config.js ──────────────────────────────────────────

def convert_tailwind_config():
    tw = TWCFG_PATH.read_text()

    # Add alpha helper at the top
    if "const a =" not in tw:
        tw = tw.replace(
            "export default {",
            'const a = (v) => `rgb(var(${v}) / <alpha-value>)`;\n\nexport default {',
        )

    # Replace brand hex values with helper
    brand_map = {
        "'#E83E8C'": "a('--color-brand-rosa')",
        "'#9D4EDD'": "a('--color-brand-morado')",
        "'#17B3B8'": "a('--color-brand-turquesa')",
        "'#FFC107'": "a('--color-brand-amarillo')",
        "'#FF6B35'": "a('--color-brand-naranja')",
    }
    for hex_str, helper in brand_map.items():
        tw = tw.replace(hex_str, helper)

    # Replace ALL 'var(--color-X)' with a('--color-X')
    # (No alpha-exempt list — all vars are now triplets)
    def replace_config_var(m):
        varname = m.group(1)
        return f"a('{varname}')"

    tw = re.sub(
        r"'var\((--color-[\w-]+)\)'",
        replace_config_var,
        tw,
    )

    # Add DEFAULT for accent (maps to primary) so bare bg-accent / ring-accent work
    tw = tw.replace(
        "accent: {\n          primary:",
        "accent: {\n          DEFAULT: a('--color-accent-primary'),\n          primary:",
    )

    # Add card alias for surface (maps to primary) so bg-surface-card works
    tw = tw.replace(
        "muted: a('--color-surface-muted'),\n          selected:",
        "muted: a('--color-surface-muted'),\n          card: a('--color-surface-primary'),\n          selected:",
    )

    TWCFG_PATH.write_text(tw)


# ─── Main ─────────────────────────────────────────────────────────────────

def main():
    print("Step 1: Converting index.css (all vars to triplets)...")
    convert_index_css()

    css = CSS_PATH.read_text()
    triplet_count = len(re.findall(r"--color-[\w-]+:\s*[\d]+ [\d]+ [\d]+", css))
    alpha_triplet = len(re.findall(r"--color-[\w-]+:\s*[\d]+ [\d]+ [\d]+ /", css))
    rgb_wrap = len(re.findall(r"rgb\(var\(--color-", css))
    print(f"  Triplet declarations (no alpha): {triplet_count - alpha_triplet}")
    print(f"  Triplet declarations (with alpha): {alpha_triplet}")
    print(f"  rgb() wrapped refs: {rgb_wrap}")

    print("\nStep 2: Converting tailwind.config.js...")
    convert_tailwind_config()

    tw = TWCFG_PATH.read_text()
    a_helper = len(re.findall(r"a\('--color-", tw))
    print(f"  Alpha-helper keys: {a_helper}")

    print("\nDone. Run build + ghost checker to verify.")


if __name__ == "__main__":
    main()
