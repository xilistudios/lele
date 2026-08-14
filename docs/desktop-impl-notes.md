# Lele Desktop — Implementation Notes (orchestrator)

Worktree: `/mnt/hdd1/Development/xilistudios/lele-desktop` (branch `feat/desktop-tauri`, from `main` @ 4491d66).
Plan: `docs/desktop-tauri-plan.md`.

## Environment constraints (IMPORTANT)
- Go, cargo, bun all available. Web build done; `cmd/lele/web/dist` populated; `go build ./cmd/lele` passes.
- Tauri on Linux needs WebKitGTK **dev** headers (`webkitgtk4-devel` on openSUSE / `libwebkit2gtk-4.1-dev` on Debian). NOT installed here and no passwordless sudo. So the Rust shell is written carefully but compiled/verified in CI, not locally. Also need `gtk3-devel`, `libsoup3-devel` for `cargo check`.
- To build locally on a proper machine: `sudo zypper in webkitgtk4-devel gtk3-devel libsoup3-devel` (or Debian equivalents), then `cd desktop && bun run tauri build`.

## Architecture decisions (final)
1. **Sidecar** = the `lele` binary itself run as `lele gateway --desktop --port 0`. Tauri spawns it, parses `LELE_READY` from stdout, navigates the webview to the reported URL.
2. **Auth (robust dual-path):** the main webview is created AFTER the sidecar is ready, with an `initialization_script` that injects `window.__LELE_DESKTOP__ = { mode, apiUrl, session:{token,refresh_token,client_id,device_name,expires} }`. This guarantees auth on any WebView engine (no reliance on remote-origin IPC). Lifecycle IPC commands (`backend_status`, `restart_backend`, `open_data_dir`, `quit_app`) are exposed as core Tauri commands and enabled for the gateway origin via `dangerousRemoteUrlIpcAccess`; frontend uses them only if `window.__TAURI_INTERNALS__` is present (graceful degradation).
3. **Splash:** an initial window shows the bundled frontend (splash) while the sidecar boots; once ready, the main window is created (with init script + gateway URL) and the splash is closed. Avoids init-script race entirely.
4. **Backend token:** Tauri generates a 256-bit token on first launch, stores in OS keychain, passes to sidecar via `LELE_DESKTOP_TOKEN` env. Gateway registers an in-memory `desktop-local` client whose token hash matches. Bind 127.0.0.1 only.

## Task decomposition (delegated to coder subagents)
Wave 1 (parallel, Go, independent files):
  - T1 pkg/server: port-0 listener refactor + ActualPort/ActualAddr + Serve/Listen + tests
  - T2 pkg/lockfile: new package (PID lock, stale recovery) + tests
  - T3 pkg/channels/auth.go: RegisterDesktopClient + tests
Wave 2 (depends on Wave 1):
  - T4 cmd/lele/gateway.go: `--desktop` flag, 127.0.0.1 bind, SIGTERM, ready line, lockfile, desktop token wiring, stderr banners + integration test
Wave 3 (frontend, independent of Go):
  - T5 web/src/lib/desktop.ts (+ tests)
  - T6 AuthContext/AppLogic hydration + disconnect overlay (+ tests)
Wave 4 (Rust shell, write-only locally):
  - T7 desktop/ scaffold (Cargo.toml, tauri.conf.json, capabilities, build.rs, icons, splash)
  - T8 main.rs + sidecar.rs
  - T9 bootstrap.rs + commands.rs + tray.rs
Wave 5 (packaging/CI):
  - T10 desktop/scripts/build-sidecar.sh + Makefile targets
  - T11 .github/workflows/desktop.yml

## Contracts (shared between tasks)
- Ready line (stdout, exactly once): `LELE_READY {"url":"http://127.0.0.1:PORT","port":PORT}`
- Error line (stdout): `LELE_ERROR {"code":"already_running","pid":PID}`
- Env to sidecar: `LELE_DESKTOP=1`, `LELE_DESKTOP_TOKEN=<hex64>`
- Flag: `lele gateway --desktop` (also honors `LELE_DESKTOP=1`), `--port 0` => dynamic.
- Frontend global: `window.__LELE_DESKTOP__ = { mode:"local", apiUrl:string, session:{token,refresh_token,expires,client_id,device_name} }`
- IPC commands (core): `backend_status`, `restart_backend`, `open_data_dir`, `quit_app`.

## Review pass (2026-08-12, orchestrator)

Bugs found & fixed during independent review (never trust subagent reports alone):

**Rust shell (verified against real tauri 2.11.5 crate sources + compile-test harness):**
- `lib.rs`: env var typo `LELE_SIDECA_BIN` → `LELE_SIDECAR_BIN`.
- `commands.rs`: `app.path()` returns `&PathResolver`, NOT a `Result` — removed bogus `if let Ok(resolver)`.
- `capabilities/desktop-remote.json`: Tauri v2 Capability schema uses `remote: { urls: [...] }`, not top-level `url`. Also added explicit `app:allow-*` permissions for all 5 IPC commands (required for remote-origin invokes from the disconnect overlay).
- `tauri.conf.json`: removed `trayIcon` config block — tray is built programmatically in `tray.rs`; both used id "main-tray" (duplicate).
- `sidecar.rs`: `SidecarHandle` needs `#[derive(Debug)]` (tests use `expect_err`). Graceful-stop test stub now traps SIGTERM and exits 0 (mimics real gateway; `status.success()` is false for signal-killed processes).
- `bootstrap.rs`: merged two env-var tests into one (process-global `LELE_CONFIG_DIR` + parallel test threads = race).
- Compile-test harness at /tmp/rust-harness (sidecar.rs + bootstrap.rs + their deps): 12/12 tests pass. Tauri-dependent modules verified by signature-checking crate sources: `navigate(Url)`, `initialization_script`, `get_webview_window`, `exit`, `default_window_icon`, `TrayIconBuilder`, keyring v3 `Entry::new -> Result`.

**Frontend:**
- `desktop.ts`: `isDesktopAuth()` now checks `session.token` too (contract injects nested session).
- `AuthContext.tsx`: `setToken` was gated on `session.refresh_token` being truthy — desktop session has empty refresh_token, so the Authorization header would never be set. Fixed to gate on token only.
- `AuthContext.tsx`: apiUrl now forced from `getDesktopApiUrl()` in desktop mode (plan §6.2).
- Disconnect overlay + `useBackendStatus` hook implemented by coder subagent (verified independently: tsc clean, 227/227 bun tests pass, Tailwind tokens exist in config).

**Backend:**
- `gateway.go`: native channel is disabled by default in config — in desktop mode it carries ALL API routes, so force `cfg.Channels.Native.Enabled = true` when desktop. Found via smoke test (404 on /api/v1/* in isolated config dir).

**End-to-end smoke test (isolated LELE_CONFIG_DIR):**
- `LELE_READY` line on stdout, logs on stderr ✓
- Bearer auth with injected token → `/api/v1/chat/sessions` 200 ✓
- `/api/v1/auth/clients` shows `desktop-local`, expires 2036 (10y) ✓
- Second instance → `LELE_ERROR {"code":"already_running","pid":...}` ✓
- SIGTERM → graceful shutdown, `gateway.lock` removed ✓
- Web UI served at `/` → 200 ✓
- Frontend rebuilt + re-embedded into Go binary after desktop changes.

**Not verifiable locally (needs WebKitGTK dev headers → CI):** full `cargo build` of the Tauri shell. All Tauri API usage signature-verified against crate sources instead.
