#!/usr/bin/env python3
"""
F0v2: Fix alpha-fija conversion.
- Alpha-fija vars → full rgb(R G B / A) (NOT bare triplet)
- Other vars → bare triplet (R G B)  
- CSS raw: unwrap rgb() for alpha-fija var usages; keep rgb() for triplet vars
- Tailwind config: alpha-fija keys → bare var(); other keys → a() helper
"""
import re, sys

# ── 14 alpha-fija vars ──
ALPHA_FIJA = {
    'color-surface-selected',
    'color-accent-muted',
    'color-accent-subtle',
    'color-border-default',
    'color-border-light',
    'color-border-strong',
    'color-state-success-light',
    'color-state-warning-light',
    'color-state-error-light',
    'color-state-info-light',
    'color-overlay',
    'color-overlay-light',
    'color-glass',
    'color-glass-border',
}

def is_alpha_fija(name: str) -> bool:
    return name.strip().lstrip('-') in ALPHA_FIJA

# ── index.css ──

def convert_index_css(src: str) -> str:
    lines = src.split('\n')
    out = []
    i = 0
    while i < len(lines):
        line = lines[i]
        
        # Match CSS variable declaration
        m = re.match(r'^(\s+)(--color-[\w-]+):\s*(.+?);$', line)
        if m:
            indent, varname, raw_val = m.group(1), m.group(2), m.group(3).strip()
            bare = varname.lstrip('-')  # e.g. 'color-border-default'
            
            if is_alpha_fija(bare):
                # ── alpha-fija: convert to full rgb(R G B / A) ──
                # Strip any existing rgb()/rgba() wrapper
                val = raw_val
                val = re.sub(r'rgba?\(\s*([^)]+?)\s*\)', lambda m2: _commas_to_spaces(m2.group(1)), val)
                
                # Normalize comma separators to spaces in remaining value
                if ',' in val and '/' not in val:
                    parts = [p.strip() for p in val.split(',')]
                    if len(parts) == 3:
                        val = ' '.join(parts)
                    elif len(parts) == 4:
                        val = f"{' '.join(parts[:3])} / {parts[3]}"
                
                out.append(f'{indent}{varname}: rgb({val});')
            else:
                # ── Regular var: convert to bare triplet ──
                val = raw_val
                
                # Strip rgb()/rgba() and convert commas → spaces
                val = re.sub(r'rgba?\(\s*([^)]+?)\s*\)', lambda m2: _commas_to_spaces(m2.group(1)), val)
                
                # Handle comma-separated like "58, 63, 70" → "58 63 70"
                if ',' in val and '/' not in val:
                    parts = [p.strip() for p in val.split(',')]
                    if len(parts) == 3:
                        val = ' '.join(parts)
                
                # Convert hex to triplet if still present
                hm = re.match(r'^#([0-9a-fA-F]{6})$', val.strip())
                if hm:
                    h = hm.group(1)
                    val = f"{int(h[0:2],16)} {int(h[2:4],16)} {int(h[4:6],16)}"
                
                out.append(f'{indent}{varname}: {val};')
            
            i += 1
            continue
        
        # ── CSS raw: unwrap rgb(var(--alpha-fija-var)) ──
        # Match both rgb(var(--X)) and rgba(var(--X))
        def unwrap_alpha(mobj):
            prefix = mobj.group(1)  # e.g. "background: " or "border: 1px solid "
            varname = mobj.group(2)  # e.g. "color-glass"
            if is_alpha_fija(varname):
                return f'{prefix}var(--{varname})'
            return mobj.group(0)  # leave unchanged
        
        line = re.sub(
            r'(rgb)a?\(var\(--(color-[\w-]+)\)\)',
            lambda m: f'var(--{m.group(2)})' if is_alpha_fija(m.group(2)) else m.group(0),
            line
        )
        
        out.append(line)
        i += 1
    
    return '\n'.join(out)


def _commas_to_spaces(s: str) -> str:
    """Convert '232, 62, 140' or '232,62,140' → '232 62 140'"""
    parts = [p.strip() for p in s.split(',')]
    return ' '.join(parts)


# ── tailwind.config.js ──

# Map: css-var-name → tw-path (for alpha-fija vars, these get bare var())
ALPHA_FIJA_TW = {
    'color-surface-selected': 'surface.selected',
    'color-accent-muted':     'accent.muted',
    'color-accent-subtle':    'accent.subtle',
    'color-border-default':   'border.DEFAULT',
    'color-border-light':     'border.light',
    'color-border-strong':    'border.strong',
    'color-state-success-light': 'state.success-light',
    'color-state-warning-light': 'state.warning-light',
    'color-state-error-light':   'state.error-light',
    'color-state-info-light':    'state.info-light',
    'color-overlay':          'overlay.DEFAULT',
    'color-overlay-light':    'overlay.light',
    'color-glass':            'glass.DEFAULT',
    'color-glass-border':     'glass.border',
}

def convert_tailwind(src: str) -> str:
    # Replace alpha-fija keys: a('--color-X') → 'var(--color-X)'
    for css_var in ALPHA_FIJA:
        src = src.replace(
            f"a('--{css_var}')",
            f"'var(--{css_var})'"
        )
    return src


if __name__ == '__main__':
    mode = sys.argv[1] if len(sys.argv) > 1 else 'both'
    
    if mode in ('css', 'both'):
        css_path = sys.argv[2] if len(sys.argv) > 2 else 'web/src/styles/index.css'
        with open(css_path) as f:
            src = f.read()
        result = convert_index_css(src)
        with open(css_path, 'w') as f:
            f.write(result)
        print(f'[css] Wrote {css_path}')
        
        # Verify: no rgb(...) wrapping alpha-fija var usages
        bad = re.findall(r'rgb(?:\()a?\(var\(--(' + '|'.join(ALPHA_FIJA) + r')\)\)', result)
        if bad:
            print(f'[css] ERROR: {len(bad)} alpha-fija vars still wrapped in rgb(): {bad}', file=sys.stderr)
            sys.exit(1)
        
        # Verify: no rgb(...) wrapping alpha-fija var DECLARATIONS (they should be rgb(...), not rgb(rgb(...)))
        bad2 = re.findall(r'rgb\(rgb\(', result)
        if bad2:
            print(f'[css] ERROR: double rgb() found', file=sys.stderr)
            sys.exit(1)
        
        # Count triplets (bare, no rgb() or /)
        triplet_count = len(re.findall(r'--color-[\w-]+:\s+\d+\s+\d+\s+\d+;', result))
        alpha_count = len(re.findall(r'--color-[\w-]+:\s+rgb\(', result))
        print(f'[css] Triplets: {triplet_count}, Alpha-fija (rgb): {alpha_count}')
    
    if mode in ('tw', 'both'):
        tw_path = sys.argv[3] if len(sys.argv) > 3 else 'web/tailwind.config.js'
        with open(tw_path) as f:
            src = f.read()
        result = convert_tailwind(src)
        with open(tw_path, 'w') as f:
            f.write(result)
        print(f'[tw] Wrote {tw_path}')
        
        # Verify: no a() calls for alpha-fija vars
        for css_var in ALPHA_FIJA:
            if f"a('--{css_var}')" in result:
                print(f'[tw] ERROR: alpha-fija var {css_var} still uses a() helper', file=sys.stderr)
                sys.exit(1)
        print(f'[tw] OK: all {len(ALPHA_FIJA)} alpha-fija keys use bare var()')
