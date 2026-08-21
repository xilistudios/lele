# TUI First-Run Onboarding Plan

**Status:** Proposed
**Branch (planned):** `feat/tui-first-run-onboarding`
**Owner:** orchestrator + coder subagents

## 1. Goal

When a user opens the TUI for the first time (no usable provider configured), launch a
guided, friendly onboarding wizard **inside the TUI** that takes them from zero to their
first working chat in ~1 minute. No CLI detour, no dead-end welcome screen.

## 2. Current State (findings)

| Piece | Status |
|---|---|
| `lele onboard` (CLI wizard) | Exists — 704-line `fmt.Scanln` Q&A in `cmd/lele/onboard.go`. Works, but must be run manually, outside the TUI. |
| `lele tui` with no config | `LoadConfig` silently returns `DefaultConfig()` (no providers) → TUI opens a welcome screen that **cannot send messages**. Dead end. |
| `/connect` flow | Exists in-TUI: modal wizard (`ModalAddProvider`, steps 0–9: name → type → key → base → model fields → review), saves via `addProvider()` + `saveConfigToDisk()`. |
| i18n | 3 locales (en/es/pt), 229 keys each, strict parity. |
| Detection hook | `cfg.Providers == nil` ⇒ unconfigured; `listProviders()` only counts providers with ≥1 model. |

**Core idea:** don't build a parallel flow. Auto-launch a guided wizard inside the TUI on
first open, built as a thin orchestration layer over the existing `/connect` machinery plus
a new welcome/language step.

## 3. Design

### Wizard steps

```
obWelcome → obLanguage → obProviderPicker → obConnect → obVerify → obDone
```

- **obWelcome** — centered logo, "Welcome! Let's get you set up (~1 min)", progress dots.
  `Enter` continues, `Esc` opens a "Skip setup" confirm (never quits the app).
- **obLanguage** — reuses the `/lang` modal list (en/es/pt).
- **obProviderPicker** — arrow-key list of `providerPresets` (OpenAI, Anthropic,
  OpenRouter, Gemini, Ollama-local, … + "Other / custom" + "Skip for now"). Per-row hints:
  "no API key needed" for Ollama, key format hint (`sk-...`) for others.
- **obConnect** — the existing `/connect` modal steps, pre-filled from the chosen preset
  (see Phase 4).
- **obVerify** — async key validation + set agent defaults + persist config/workspace.
- **obDone** — green ✓ summary (provider, model, masked key), quick-tips cheat sheet
  (`Enter` send, `/models`, `/agents`, `ctrl+s` chats, `Shift+click` select text),
  "Press Enter to start chatting" → clears onboarding, focuses chat input.

### Invariants

1. **Reuse, don't fork** `/connect`. Extract its setup into a shared entry point.
2. Onboarding is gated by a single flag (`onboardingActive`); existing users see zero change.
3. Key validation is async (`tea.Cmd`) — the TUI must never block.
4. `Esc` skips setup, never exits the app.
5. All copy goes through i18n keys `tui.onboard.*` in en/es/pt (keep locale files at equal
   line count).

## 4. Phases (one atomic coder task each)

### Phase 1 — Detection & state
**Files:** `pkg/config/config.go`, `pkg/tui/types.go`, `pkg/tui/model.go`
- Add `func (c *Config) HasUsableProvider() bool` — true iff ≥1 named provider has
  (API key set OR local type) AND ≥1 model. Pure function, unit-testable.
- Add to `Model`: `onboardingActive bool`, `onboardingStep onboardStep` enum,
  `obSelectedPreset int`.
- In `NewModel`: if `!cfg.HasUsableProvider()` → `onboardingActive = true`,
  `showWelcome = true`.
- **Acceptance:** `lele tui` on empty config enters onboarding; existing configs
  unaffected (regression test).

### Phase 2 — Welcome + language screen
**Files:** `pkg/tui/view.go` (new `renderOnboarding()` branch), `pkg/tui/handlers.go`,
i18n locales
- When `onboardingActive && onboardingStep == obWelcome`: render centered logo + welcome
  copy + progress dots (step 1/4).
- Keys: `Enter` → language picker (reuse `/lang` modal list), `Esc` → "Skip setup" confirm.
- All copy via i18n keys `tui.onboard.*` in en/es/pt.

### Phase 3 — Provider preset picker
**Files:** `pkg/tui/view.go`, `pkg/tui/handlers.go`; reuses `providerPresets` from
`pkg/tui/provider_flow.go`
- Arrow-key list of presets + "Other / custom" + "Skip for now".
- Friendly hints per row (local = no key; key format hints for cloud providers).
- `Enter` → Phase 4 with preset pre-selected.

### Phase 4 — Guided connect (reuse, don't fork)
**Files:** `pkg/tui/commands.go` (extract entry point), `pkg/tui/handlers.go`
- Extract the `/connect` setup block into `startConnectFlow(preset *providerPreset)`;
  `/connect` calls it with `nil`, onboarding calls it with the chosen preset.
- **Pre-fill everything possible:** provider name, type, API base from preset; skip
  name/type steps entirely when a preset is given → user types only the API key and
  confirms.
- **Pre-fill model defaults** per preset (e.g. OpenAI → alias `gpt-4o`, model `gpt-4o`);
  user can press Enter through model steps. Target first-run completion ≈ paste key +
  3× Enter.
- On save (`providerSavedInFlow` + model saved): if onboarding active → go to Phase 5
  instead of closing the modal.

### Phase 5 — Verify, set defaults & success screen
**Files:** `pkg/tui/handlers.go`, `pkg/tui/provider_helpers.go`, `cmd/lele/shared.go`
(move `createWorkspaceTemplates` to `pkg/config` or a new `pkg/workspace` so the TUI can
call it; cmd keeps a wrapper)
- Async key validation via `tea.Cmd` (port of `validateProvider` from `onboard.go`,
  non-blocking; show spinner; treat failure as a warning, not a blocker — mirrors CLI).
- Set `cfg.Agents.Defaults.Provider/Model` to the freshly connected `provider:alias`.
- Persist: `saveConfigToDisk()` + `createWorkspaceTemplates(cfg.WorkspacePath())` +
  ensure logs dir.
- Success screen: green ✓ summary (provider, model, masked key), quick-tips cheat sheet,
  "Press Enter to start chatting" → clears `onboardingActive`, focuses chat input.

### Phase 6 — i18n + polish
- ~25 new `tui.onboard.*` keys × 3 locales; keep files at equal line count.
- Progress indicator (● ● ○ ○) across all steps; consistent `ModalContainer` styling;
  graceful wrapping on small terminals (reuse the welcome-screen 60-col box).

### Phase 7 — Tests
- `config.HasUsableProvider` table tests (nil providers / key-less / local / with models).
- Wizard transition tests in the style of `connect_flow_test.go` /
  `connect_render_test.go`: first-run triggers onboarding; preset selection pre-fills
  steps; skip path; success path sets agent defaults + saves.
- Regression: config with providers → no onboarding.

### Phase 8 — Follow-ups (optional, separate PR)
- `/setup` command to re-run the wizard anytime.
- Auto-detect running Ollama (`localhost:11434/v1/models`) and offer one-key local setup.
- Make bare `lele` (no args, no config) launch the TUI onboarding instead of printing help.

## 5. Sequencing

```
P1 (detection) → P2 (welcome) → P3 (picker) → P4 (connect reuse) → P5 (verify/save) → P6 (i18n) → P7 (tests)
```

Each phase = one atomic coder task, independently compilable and testable. P1–P3 land
safely behind the `onboardingActive` flag (zero behavior change for existing users).

## 6. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Forking `/connect` → two wizards drift | Extract a shared `startConnectFlow()` entry point; `/connect` becomes a thin caller. |
| Key validation blocks the TUI | Run via `tea.Cmd` with a spinner; failure is a warning, not a blocker. |
| `Esc` accidentally quits the app | Esc only opens a "Skip setup" confirm; never maps to `tea.Quit` during onboarding. |
| Config saved to wrong path | `saveConfigToDisk()` uses `config.DefaultConfigPath()`, which already honors `LELE_CONFIG_DIR`. |
| i18n locale drift | Add all `tui.onboard.*` keys to en/es/pt in the same task; keep equal line counts. |
| Existing users see onboarding | Gated strictly on `HasUsableProvider()`; regression test in Phase 7. |

## 7. Acceptance Criteria

1. Fresh install → `lele tui` opens directly into the onboarding wizard (no CLI step).
2. User can connect a provider by pasting an API key and pressing Enter ≤ 3 times.
3. Ollama path works with zero credentials.
4. After success, the TUI lands on a normal chat screen with provider/model defaults set,
   config persisted, and workspace templates created.
5. "Skip for now" exits cleanly to the regular welcome screen.
6. Existing configured installs show no onboarding (regression-tested).
7. All wizard copy is localized in en/es/pt.
