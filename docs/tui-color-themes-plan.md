# TUI Color Themes — Implementation Plan

**Status:** DRAFT
**Branch:** `feat/tui-color-themes`
**Date:** 2026-08-20

## Goal

User-selectable color themes for the TUI with:

1. A dedicated **`tui.json`** config file (separate from `config.json`) holding the
   selected theme name and any user-defined custom themes.
2. Theme selection in the **onboarding wizard** (new step after language).
3. Theme selection in the **Settings → Interface** page at any time.
4. Live apply — no restart needed.

Non-goals (this iteration): WebUI theming, per-session themes, a `/theme` slash
command (future work), theme import/export UI.

---

## 1. Theme Model

A theme is a palette of the **11 semantic colors** already used by `style.go`:

| JSON key             | Current var      | Dracula default | Role                          |
|----------------------|------------------|-----------------|-------------------------------|
| `background`         | `BgColor`        | `#181824`       | App background (paintFrame)   |
| `input_background`   | `InputBgColor`   | `#212130`       | Input bar / boxes / modals    |
| `primary`            | `PrimaryColor`   | `#FF5555`       | Red — errors, logo, rejected  |
| `secondary`          | `SecondaryColor` | `#50FA7B`       | Green — success, assistant    |
| `accent`             | `AccentColor`    | `#8BE9FD`       | Cyan — user role, links       |
| `purple`             | `PurpleColor`    | `#BD93F9`       | Purple — titles, tools, group |
| `orange`             | `OrangeColor`    | `#FFB86C`       | Orange — thinking, tool calls |
| `comment`            | `CommentColor`   | `#6272A4`       | Muted — borders, hints        |
| `foreground`         | `Foreground`     | `#F8F8F2`       | Main text                     |
| `selection_background` | `SelectionBg`  | `#44475A`       | Active item highlight         |
| `yellow`             | `YellowColor`    | `#F1FA8C`       | Warnings, running status      |

Color values accept anything `lipgloss.Color` understands: `"#RRGGBB"`, `"#RGB"`,
or ANSI-256 numbers as strings (`"39"`, `"240"`). Validation parses via lipgloss
render probe; invalid values fall back to the Dracula default for that field
(never a hard failure).

### Built-in themes (in code)

| Name              | Notes                                    |
|-------------------|------------------------------------------|
| `dracula`         | Current palette — the default            |
| `nord`            | Polar night dark                         |
| `catppuccin`      | Mocha dark                               |
| `gruvbox`         | Dark, warm                               |
| `tokyo-night`     | Dark blue                                |
| `solarized-light` | Light theme (validates bg-reapply logic) |

Custom themes in `tui.json` may be **partial** — missing fields inherit from the
Dracula palette.

---

## 2. `tui.json` — File Format & Location

Location: `<GetLeleDir()>/tui.json` (same dir as `config.json`, honors
`LELE_CONFIG_DIR`). Path helper: `theme.DefaultPath()`.

```json
{
  "theme": "dracula",
  "custom_themes": {
    "ocean": {
      "background": "#0a192f",
      "input_background": "#112240",
      "accent": "#64ffda"
    }
  }
}
```

Resolution order for `"theme"`:
1. `custom_themes[name]` (user overrides win)
2. built-in registry
3. fallback: `dracula` (log/track the bad name; surface in settings row)

Rules:
- File missing → all defaults, no error, nothing written until the user picks a theme.
- Malformed JSON → fall back to defaults entirely (do not crash the TUI).
- Save writes pretty-printed JSON (`json.MarshalIndent`, 2 spaces), atomic
  (write temp + rename) to avoid truncation on crash.
- `config.json` `TUIConfig` is untouched (mouse/maxMessages/streamThrottle stay there).

---

## 3. Architecture

### 3.1 New package `pkg/tui/theme/`

```
pkg/tui/theme/
├── theme.go    — Theme struct (11 fields), Palette resolution, Validate
├── builtin.go  — built-in registry (6 themes)
├── store.go    — Load(path), Save(path), DefaultPath(), file struct
├── theme_test.go
├── builtin_test.go
└── store_test.go
```

Key API:

```go
type Theme struct { Background, InputBackground, Primary, Secondary, Accent,
                     Purple, Orange, Comment, Foreground, SelectionBackground,
                     Yellow string }

func Builtins() []string                     // sorted names
func Get(name string, custom map[string]Theme) Theme // resolve + fallbacks
func (t *Theme) Normalize()                  // fill empty fields from Dracula, validate
func Load(path string) (name string, custom map[string]Theme, err error)
func Save(path, name string, custom map[string]Theme) error
func DefaultPath() string
```

`theme` returns resolved colors as strings; conversion to `lipgloss.Color`
happens in the TUI layer (keeps the package UI-framework-free and testable).

### 3.2 `style.go` refactor — rebuildable styles

Keep **all existing var names** (zero call-site churn across ~15 files). Change
initialization from inline expressions to a rebuild function:

```go
// ApplyTheme swaps the active palette: reassigns the 11 color vars, rebuilds
// every derived style var, and regenerates pre-rendered artifacts.
func ApplyTheme(t theme.Theme) { ... }   // calls rebuildStyles()
```

- Move every current style initializer body into `rebuildStyles()` (mechanical
  move; the `var (...)` block keeps declarations, initializers become zero values
  populated by `rebuildStyles()` called from `init()` with Dracula).
- Regenerate `bouncingDotChar = BouncingDot.Render("●")` (pre-rendered at init).
- `reapplyBackground` already reads `BgColor` dynamically via
  `backgroundOpenSeq(BgColor)` — works as-is once the var is reassigned.
- **Concurrency note:** bubbletea renders `View()` on the renderer goroutine
  while `Update()` applies the theme. This is a benign one-shot race (worst case:
  one mixed frame) consistent with existing practice (`m.width` is already read
  in `View()` and written in `Update()` unlocked). No locks added; documented.

### 3.3 Hardcoded colors pulled into the palette

| File           | Hardcode                     | Becomes                    |
|----------------|------------------------------|----------------------------|
| `markdown.go:51` | `lipgloss.Color("39")`     | `AccentColor`              |
| `model.go:83,98,99` | `"240"` (placeholders)  | `CommentColor` (fg-only)   |
| `selection.go:21` | `#264f78`                 | `SelectionBg`              |

⚠️ Constraint from the color-band fix: textarea/textinput sub-styles must stay
**fg-only** (bubbles stock defaults emit raw ANSI backgrounds that break
`reapplyBackground`). Extract `model.go` input styling into
`(m *Model) applyThemeToInputs()` called at init **and** on every theme change.

### 3.4 Render-cache invalidation on theme change

`viewport.go` caches rendered message lines. On theme switch the Model must:

```go
m.msgRenderCacheLines = nil
m.msgRenderCacheWidth = 0
m.renderedBaseValid = false
```

(plus `ApplyTheme` + `applyThemeToInputs` + `tui.json` persist). Bundle into a
single `(m *Model) applyThemeByName(name string) error` helper in a new file
`pkg/tui/theme_apply.go`.

---

## 4. Settings Page Integration (Interface sub-menu)

`settings_tui.go` changes:

- `loadTUISettings()` prepends a row: `Theme: <current>` → new index layout:
  `0=theme, 1=mouse, 2=maxMessages, 3=streamThrottle` (update
  `handleTUISettingsEnter` switch accordingly).
- Enter on the Theme row → **picker mode**: `m.themePickerActive = true`
  (new `types.go` field), `m.modalItems` = sorted theme list (built-ins +
  custom names), current one prefixed `•`. Reuses existing up/down/enter list
  navigation in `handlers.go`.
- Enter on a theme → `m.applyThemeByName(name)` (live apply + persist
  `tui.json`), exit picker, `loadTUISettings()` refresh.
- Esc in picker mode → back to the Interface list without changing the saved
  theme (if the user preview-applied, keep last-applied; simplest contract:
  apply-on-enter only, Esc never persists).
- i18n keys (en/es/pt): `tui.settings.theme`, `tui.settings.themePickerTitle`,
  `tui.settings.themeCustomSuffix` (e.g. `" (custom)"`).

---

## 5. Onboarding Integration

Insert a new step **after language**: flow becomes

```
obWelcome → obLanguage → obTheme → obProviderPicker → obConnect → obVerify → obDone
```

- `types.go`: add `obTheme` to the `onboardStep` iota (named constants keep all
  switch sites compile-safe; values shift — no logic depends on raw numbers).
- Step count 5 → **6**: update every `renderProgressDots(n)` call and
  `tui.onboard.progress` format args (`"Step %d of %d"`) in `view.go`.
- `renderObTheme(width)` mirrors `renderObLanguage`: title, progress dots
  (step 3 of 6), theme list with `modalSelectedIdx`, keyboard hints. Default
  selection = current theme (so one Enter continues with defaults).
- `handlers.go` `obTheme` case: up/down navigate; enter →
  `m.applyThemeByName(...)` + persist + advance to `obProviderPicker`;
  esc → back to `obLanguage`.
- i18n keys: `tui.onboard.theme` (title), `tui.onboard.themeHint`.
- **`onboarding_test.go` (1175 lines) must be updated**: every flow test that
  walks language → provider picker now crosses the theme step. Add dedicated
  tests: theme applied + persisted during onboarding, esc back-navigation,
  skip path unaffected.

---

## 6. Phases (atomic coder tasks)

Each phase = one focused task, independently verifiable (`go build ./... &&
go test ./pkg/tui/... ./pkg/tui/theme/...` green before moving on).

### Phase 1 — `pkg/tui/theme` package (pure, no TUI deps)
Theme struct, Dracula defaults, 6 built-ins, Normalize/validate with per-field
fallback, Load/Save/DefaultPath (atomic write), resolution order.
**Tests:** round-trip, missing file, malformed JSON, partial custom theme,
invalid color fallback, bad name → dracula.

### Phase 2 — `style.go` rebuild refactor
Convert style var block to declarations + `rebuildStyles()` + `ApplyTheme()`
(consuming `theme.Theme`); regenerate `bouncingDotChar`; replace hardcoded
colors in `markdown.go` / `selection.go`. Behavior-identical for Dracula.
**Tests:** existing `frame_bg_test.go` + `paintframe_equiv_test.go` must pass
unchanged; new test: ApplyTheme(nord) changes `BgColor` + `AppContainer`
render output + `reapplyBackground` uses new bg seq; ApplyTheme(dracula)
restores byte-identical frames.

### Phase 3 — Model wiring
`theme_apply.go` with `applyThemeByName` (ApplyTheme + `applyThemeToInputs` +
cache invalidation + persist); load `tui.json` in `NewModel` (before first
render); extract input styling from `model.go` (fg-only constraint).
**Tests:** startup with missing/valid/invalid tui.json; cache invalidated after
switch (rendered lines differ); inputs restyled.

### Phase 4 — Settings → Interface theme picker
Row prepend + index shift, `themePickerActive` mode, navigation/enter/esc in
`handlers.go`, i18n keys ×3 locales.
**Tests:** settings list shows current theme; enter opens picker; selecting
applies + persists file; esc cancels; index shift didn't break mouse/edit rows.

### Phase 5 — Onboarding theme step
`obTheme` step, render fn, handler case, progress dots 5→6, i18n keys,
**full `onboarding_test.go` update** + new theme-step tests.
**Tests:** full wizard walk with theme step; theme persisted on select;
back-navigation chain intact.

### Phase 6 — Docs & polish
README section (tui.json format + example), `CHANGELOG.md` entry, verify
`solarized-light` renders correctly end-to-end (bg reapply on light bg).

---

## 7. Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Onboarding test suite (1175 lines) breaks on step insertion | Phase 5 explicitly owns the test updates; named iota constants keep it compile-safe |
| `reapplyBackground` bands on light themes | Phase 6 manual verification with `solarized-light`; logic is bg-agnostic by design |
| Stale render cache shows old colors | Phase 3 invalidates all three cache fields; regression test |
| Pre-rendered `bouncingDotChar` keeps old color | Regenerated inside `ApplyTheme` |
| Bubbles widgets re-emit raw ANSI bgs | Keep fg-only sub-style constraint in `applyThemeToInputs` |
| Corrupt `tui.json` crashes startup | Load never returns fatal; defaults + continue |
| View/Update goroutine race on style vars | Accepted benign one-shot race (matches existing `m.width` practice); documented |

## 8. Acceptance Criteria

- [ ] `~/.lele/tui.json` created on first theme selection; hand-editable.
- [ ] 6 built-in themes switchable live from Settings → Interface.
- [ ] Custom partial theme in `tui.json` renders with Dracula fallbacks.
- [ ] Onboarding offers theme choice (step 3 of 6) and persists it.
- [ ] No restart needed; no color bands; message history re-renders fully.
- [ ] `go test ./...` green; no Dracula visual regression (byte-identical frames).
