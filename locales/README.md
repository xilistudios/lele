# Language packs (lazy download)

The lele binary embeds only three UI locales (`es`, `en`, `pt`) so binary size
stays constant. Additional languages live in this directory and are downloaded
on demand from GitHub into `~/.lele/locales/`.

## Layout

```
locales/
  index.json       # catalog of available languages
  tui/<code>.json  # TUI strings (flat map, same shape as pkg/tui/i18n/locales)
  web/<code>.json  # WebUI strings (nested i18next resource)
```

## How it works

1. TUI `/lang` or Settings → Language lists builtins + any installed packs,
   plus **Download more languages…**.
2. Selecting a remote language fetches `locales/index.json` and
   `locales/{tui,web}/<code>.json` from
   `https://raw.githubusercontent.com/xilistudios/lele/main/locales/...`
3. Packs are cached under `~/.lele/locales/{tui,web}/` and merged at startup.
4. WebUI Settings → Language loads the catalog from `GET /api/v1/locales` and
   merges packs via `POST /api/v1/locales/{code}/install` +
   `GET /api/v1/locales/{code}`.

## Adding a language

1. Copy `pkg/tui/i18n/locales/en.json` → `locales/tui/<code>.json` and translate values.
2. Copy `web/src/i18n/locales/en.json` → `locales/web/<code>.json` and translate values.
3. Add an entry to `locales/index.json` with `"builtin": false`.
4. Keep all keys and placeholders (`%s`, `{{count}}`, …) unchanged.
5. Validate: `python3 -c "import json; json.load(open('locales/tui/xx.json'))"`

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/locales` | Catalog + install flags |
| GET | `/api/v1/locales/{code}` | Web pack JSON (JIT install if missing) |
| POST | `/api/v1/locales/{code}/install` | Download pack from GitHub |
| DELETE | `/api/v1/locales/{code}` | Remove cached pack |
| POST | `/api/v1/locales/refresh` | Refresh catalog from GitHub |

## Notes

- Builtin languages never download; they stay in the binary.
- Missing keys fall back to Spanish, then the key itself (existing behavior).
- Network failures leave the UI on installed/builtin languages.
