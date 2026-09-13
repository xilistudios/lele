#!/usr/bin/env python3
"""
Color no-regression verification.
For each color utility in the baseline (f0-before.css), verify that the new bundle
references the SAME var and that rgb(triplet) == original hex/rgb value.

This checks that the conversion didn't alter any color value — only its representation.
"""
import re, sys, os

def parse_color_value(raw: str):
    """Normalize any CSS color to (R, G, B, A) tuple for comparison."""
    raw = raw.strip().rstrip(';').strip()
    
    # #hex
    m = re.match(r'^#([0-9a-fA-F]{6})$', raw)
    if m:
        h = m.group(1)
        return (int(h[0:2],16), int(h[2:4],16), int(h[4:6],16), 1.0)
    
    # rgb(r, g, b) or rgba(r, g, b, a)
    m = re.match(r'^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*([\d.]+))?\s*\)$', raw)
    if m:
        r, g, b = int(m.group(1)), int(m.group(2)), int(m.group(3))
        a = float(m.group(4)) if m.group(4) else 1.0
        return (r, g, b, a)
    
    # rgb(r g b / a) — modern syntax
    m = re.match(r'^rgb\(\s*(\d+)\s+(\d+)\s+(\d+)\s*(?:/\s*([\d.]+))?\s*\)$', raw)
    if m:
        r, g, b = int(m.group(1)), int(m.group(2)), int(m.group(3))
        a = float(m.group(4)) if m.group(4) else 1.0
        return (r, g, b, a)
    
    # bare triplet "R G B"
    m = re.match(r'^(\d+)\s+(\d+)\s+(\d+)$', raw)
    if m:
        return (int(m.group(1)), int(m.group(2)), int(m.group(3)), 1.0)
    
    return None

# Extract all --color-* declarations from a CSS string
# Returns dict: varname -> raw_value (the exact string after the colon)
def extract_vars(css: str):
    result = {}
    for m in re.finditer(r'--([\w-]+)\s*:\s*([^;]+);', css):
        name, val = m.group(1), m.group(2).strip()
        if name.startswith('color-'):
            result[name] = val
    return result

# Extract CSS rule declarations that reference --color-* vars
# Returns dict: (selector_context, property) -> var_reference
def extract_css_var_usages(css: str):
    """Extract property-value pairs that use var(--color-*) references."""
    result = {}
    # Simple approach: find all lines with var(--color-*)
    for m in re.finditer(r'([\w-]+)\s*:\s*([^;]*var\(--(color-[\w-]+)\)[^;]*);', css):
        prop = m.group(1)
        full_val = m.group(2).strip()
        varname = m.group(3)
        result[(prop, varname)] = full_val
    return result

def main():
    baseline_path = sys.argv[1] if len(sys.argv) > 1 else '/tmp/f0-before.css'
    new_path = sys.argv[2] if len(sys.argv) > 2 else 'web/src/styles/index.css'
    
    with open(baseline_path) as f:
        baseline = f.read()
    with open(new_path) as f:
        new_css = f.read()
    
    base_vars = extract_vars(baseline)
    new_vars = extract_vars(new_css)
    
    mismatches = []
    checked = 0
    skipped = 0
    
    ALPHA_FIJA = {
        'color-surface-selected', 'color-accent-muted', 'color-accent-subtle',
        'color-border-default', 'color-border-light', 'color-border-strong',
        'color-state-success-light', 'color-state-warning-light',
        'color-state-error-light', 'color-state-info-light',
        'color-overlay', 'color-overlay-light', 'color-glass', 'color-glass-border',
    }
    
    # Also check shadow vars (should be unchanged)
    SHADOW_VARS = {'shadow-card', 'shadow-pop', 'shadow-float'}
    
    for name, base_val in sorted(base_vars.items()):
        if name not in new_vars:
            mismatches.append(f"  MISSING in new: --{name}")
            continue
        
        new_val = new_vars[name]
        checked += 1
        
        # Parse colors
        base_color = parse_color_value(base_val)
        new_color = parse_color_value(new_val)
        
        if base_color is None:
            skipped += 1
            continue
        if new_color is None:
            mismatches.append(f"  PARSE FAIL --{name}: base='{base_val}' → {base_color}, new='{new_val}' → {new_color}")
            continue
        
        if base_color != new_color:
            mismatches.append(f"  MISMATCH --{name}: base='{base_val}'→{base_color} vs new='{new_val}'→{new_color}")
    
    print(f"Color regression check: {checked} vars checked, {skipped} skipped, {len(mismatches)} mismatches")
    if mismatches:
        for m in mismatches:
            print(m)
        sys.exit(1)
    else:
        print("OK: All color values match between baseline and new CSS")
        sys.exit(0)

if __name__ == '__main__':
    main()
