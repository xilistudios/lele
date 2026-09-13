#!/usr/bin/env python3
"""
Check for double-slash CSS — rgb(rgb(...)) or rgb(var(--alpha-fija) / ...) in bundle.
This script verifies that alpha-fija vars are NOT wrapped in rgb() either in:
  1. index.css raw CSS rules
  2. dist/*.css bundle (where Tailwind outputs generated utilities)
"""
import re, sys, glob, os

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

pat = re.compile(r'rgb(?:a)?\(var\(--(' + '|'.join(sorted(ALPHA_FIJA)) + r')\)')

errors = []

# Check index.css
css_path = sys.argv[1] if len(sys.argv) > 1 else 'web/src/styles/index.css'
with open(css_path) as f:
    css_src = f.read()

matches = pat.findall(css_src)
if matches:
    for m in set(matches):
        count = matches.count(m)
        errors.append(f"  index.css: rgb(var(--{m})) found {count}x")

# Check bundle
dist_dir = sys.argv[2] if len(sys.argv) > 2 else 'web/dist'
for fpath in glob.glob(os.path.join(dist_dir, '**/*.css'), recursive=True):
    with open(fpath) as f:
        bundle = f.read()
    matches = pat.findall(bundle)
    if matches:
        for m in set(matches):
            count = matches.count(m)
            fname = os.path.basename(fpath)
            errors.append(f"  {fname}: rgb(var(--{m})) found {count}x")

if errors:
    print(f"DOUBLE-SLASH RISK: {len(errors)} violations found:")
    for e in errors:
        print(e)
    sys.exit(1)
else:
    print("OK: 0 double-slash violations (alpha-fija vars are NOT wrapped in rgb())")
    sys.exit(0)
