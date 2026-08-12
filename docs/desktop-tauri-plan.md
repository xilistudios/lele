# Lele Desktop (Tauri) — Implementation Plan

**Status:** Draft
**Date:** 2026-08-12
**Author:** lele (software-engineer)

## 1. Goal

Ship a native desktop application for lele (macOS / Windows / Linux) that wraps
the existing gateway + Web UI in a single double-clickable app, using Tauri v2.

Non-goals for v1:
- Mobile (Tauri v2 supports it, but out of scope).
- Rewriting the UI. The existing React Web UI is reused as-is.
- Replacing the CLI / headless gateway. Desktop is an additional frontend.

## 2. Current Architecture (facts that shape the design)

| Fact | Implication |
|---|---|
| `lele gateway` serves REST API + WebSocket + embedded Web UI on one port (default 3005) | The desktop app only needs a window pointing at a local gateway URL. |
| Web UI is embedded via `go:embed web/dist` | No asset shipping problem; the sidecar binary is self-contained. |
| Auth = PIN pairing (`POST /auth/pin` → `POST /auth/pair` → bearer token + refresh token) | Desktop needs a frictionless local auth path; PIN UX is unacceptable for a local app. |
| SQLite via `modernc.org/sqlite`, `CGO_ENABLED=0` | Go binary cross-compiles cleanly for all Tauri targets. No CGO toolchain issues. |
| `/health` and `/ready` endpoints exist | Ready-made readiness probe for sidecar startup. |
| CORS middleware + token auth on `/api/v1/*` | Loading the UI from the gateway's own origin avoids CORS entirely. |
| goreleaser already builds web + binary for many OS/arch | Desktop packaging can reuse the same build recipe per target triple. |

## 3. Architecture Decision: Sidecar (Option A)

### Option A — Tauri shell + `lele` binary as sidecar (CHOSEN)

```
┌─────────────────────────────────────────────┐
│ Tauri App (Rust core + system WebView)      │
│                                             │
│  ┌───────────────┐      spawns/manages      │
│  │ Rust backend  │ ─────────────────────┐   │
│  │ (lifecycle,   │                      ▼   │
│  │  tray, auth   │              ┌──────────┐│
│  │  bootstrap)   │              │ lele     ││
│  └───────┬───────┘              │ gateway  ││
│          │ injects token        │ :PORT    ││
│          ▼                      │ (sidecar)││
│  ┌───────────────┐   HTTP/WS    └────▲─────┘│
│  │ WebView       │ ──────────────────┘      │
│  │ (existing     │   http://127.0.0.1:PORT  │
│  │  React WebUI) │                          │
│  └───────────────┘                          │
└─────────────────────────────────────────────┘
```

- Tauri spawns the `lele` binary as a **sidecar process** (Tauri has first-class
  sidecar support: bundling per target-triple, stdout/stderr pipes, lifecycle).
- The WebView loads `http://127.0.0.1:<port>/` — the gateway's own embedded UI.
  Same origin ⇒ no CORS, no asset duplication, Web UI code unchanged except a
  small desktop bootstrap adapter.
- Rust side owns: process lifecycle, port discovery, token bootstrap, tray,
  single-instance, graceful shutdown.

**Why not Option B (embed Go as a library inside the Rust process):** Go builds
position-dependent binaries with a goroutine runtime; embedding into a Rust
host via cgo is fragile, breaks `CGO_ENABLED=0`, complicates crashes/updates,
and duplicates lifecycle logic. The sidecar approach keeps lele's runtime
exactly as it is today (same code path as CLI/server users), makes the backend
independently updatable, and is the standard Tauri pattern.

**Why Tauri over Electron/Wails:** ~10-15 MB installer vs ~100 MB+, uses the
system WebView (WebView2/WKWebKit/WebKitGTK), Rust shell is small, first-class
sidecar + tray + updater + single-instance plugins. (Wails is Go-native but has
weaker sidecar/tray story and would still need a window shell around the same
gateway; Electron rejected on size/memory.)

## 4. Backend Changes Required (lele core)

All changes are additive and must not affect CLI/headless behavior.

### 4.1 Desktop launch mode (`lele gateway --desktop`)

New flag (or `LELE_DESKTOP=1` env) that enables:

1. **Bind to `127.0.0.1` only** (never `0.0.0.0`) regardless of config default.
2. **Dynamic port**: support `--port 0` → OS-assigned free port. Requires a
   small refactor in `pkg/server`: use `net.Listen("tcp", addr)` first, read
   the actual port from `listener.Addr()`, then `srv.Serve(listener)`.
3. **Machine-readable ready signal** on stdout:
   ```
   LELE_READY {"url":"http://127.0.0.1:54321","port":54321}
   ```
   Printed once the server is listening. Tauri parses stdout lines; falls back
   to polling `/health` if the line is missed. Human-readable startup banner
   goes to stderr in desktop mode (Tauri captures both).
4. **Desktop token bootstrap** (see §5): accept `LELE_DESKTOP_TOKEN` env var.
5. **SIGTERM handling**: gateway currently handles `os.Interrupt` (SIGINT).
   Add SIGTERM to the signal set so Tauri's graceful kill works
   (`cmd/lele/gateway.go` signal.Notify).
6. **Lockfile / single-instance guard**: `~/.lele/gateway.lock` (PID file with
   stale detection). If a gateway is already running, exit with a
   machine-readable error line `LELE_ERROR {"code":"already_running",...}` so
   the desktop app can offer "connect to running instance" instead of hanging.

### 4.2 Desktop auto-auth

New trusted-local auth path, active only in desktop mode:

- Tauri generates a random 256-bit token at first launch, stores it in the OS
  keychain (via `tauri-plugin-stronghold` or keyring crate) AND passes it to
  the sidecar as `LELE_DESKTOP_TOKEN`.
- `AuthManager` (pkg/channels/auth.go), when started in desktop mode with that
  env var set, registers a built-in client `desktop-local` whose valid token
  equals the env value (plus a matching refresh token). `ValidateToken`
  accepts it like any paired client.
- The token never appears in stdout/logs/URLs. Rotation: desktop app can call
  the existing refresh endpoint; re-pairing on token loss = regenerate + restart
  sidecar (rare).

Fallback kept intact: if a user points the desktop app at a *remote or already
running* gateway, the existing PIN pairing UI works unchanged.

### 4.3 Estimated backend effort

| Change | Files | Size |
|---|---|---|
| Port-0 + listener refactor + ready line | `pkg/server/server.go`, `cmd/lele/gateway.go` | S |
| Desktop mode flag + 127.0.0.1 bind | `cmd/lele/gateway.go`, `pkg/config` | S |
| SIGTERM | `cmd/lele/gateway.go` | XS |
| Desktop token in AuthManager | `pkg/channels/auth.go`, `native.go` | S |
| Lockfile guard | `cmd/lele/gateway.go` (new `pkg/lockfile`?) | S |

## 5. Tauri Application

### 5.1 Layout

```
desktop/
├── src-tauri/
│   ├── Cargo.toml
│   ├── tauri.conf.json        # window, bundle, sidecar config
│   ├── capabilities/          # Tauri v2 permission caps
│   ├── icons/
│   ├── binaries/              # lele-<target_triple> sidecars (build artifact)
│   └── src/
│       ├── main.rs
│       ├── sidecar.rs         # spawn, stdout parse, health poll, shutdown
│       ├── bootstrap.rs       # token mgmt + webview init script
│       ├── tray.rs
│       └── commands.rs        # IPC commands exposed to frontend
├── package.json               # tauri CLI scripts only (frontend = web/)
└── scripts/
    └── build-sidecar.sh       # cross-compile Go binary → binaries/
```

The frontend is **`web/`** (existing app). `tauri.conf.json` points
`build.frontendDist` at `web/dist` for dev convenience, but at runtime the
window navigates to the sidecar URL (the gateway-served UI), not the bundled
static copy — this guarantees UI/backend version match. The bundled copy is
only used as an offline splash/loading screen shown while the sidecar boots.

### 5.2 Startup sequence

1. App starts → Rust checks lockfile / probes default port for an existing
   gateway.
   - **Existing gateway found** → show connect screen (PIN flow, reuse Web UI)
     or auto-connect if it's a desktop-managed instance.
   - **None** → spawn sidecar: `lele gateway --desktop --port 0` with
     `LELE_DESKTOP_TOKEN=<token>` env.
2. Parse `LELE_READY` from stdout (timeout 30 s, fallback: poll `/health`
   every 250 ms).
3. Create window with **initialization script** (Tauri v2
   `windowBuilder.initializationScript`) that sets:
   ```js
   window.__LELE_DESKTOP__ = { mode: "local", apiUrl: "http://127.0.0.1:PORT" };
   // token injected via IPC invoke, not in the script source
   ```
4. Navigate to `http://127.0.0.1:PORT/`.
5. Web UI bootstrap (see §6) detects `__LELE_DESKTOP__`, calls
   `invoke("plugin:lele-desktop|get_session")` to retrieve the token, builds
   the AuthSession, skips the PIN screen.

### 5.3 Shutdown & lifecycle

- Window close → hide to tray (macOS/Windows convention), sidecar keeps
  running (so cron/heartbeat/Telegram keep working). Configurable:
  "quit on close" option.
- Quit (tray → Quit, or Cmd+Q twice) → send SIGTERM to sidecar → wait ≤ 5 s →
  SIGKILL. Flush session state is already handled by gateway's signal path.
- Sidecar crash/exit detected → Rust emits event to webview → Web UI shows a
  "backend disconnected" overlay with Restart button (the WS client already
  has reconnect logic; this covers process death).
- App single-instance via `tauri-plugin-single-instance`; second launch focuses
  the existing window.

### 5.4 IPC surface (Rust → JS commands)

| Command | Purpose |
|---|---|
| `get_session` | Return `{ apiUrl, token, refresh_token, client_id, device_name }` |
| `refresh_session` | Re-read token after refresh (kept in sync) |
| `backend_status` | sidecar pid, port, uptime, health |
| `restart_backend` | Kill + respawn sidecar |
| `open_data_dir` | Open `~/.lele` in file manager |
| `quit_app` | Full quit with sidecar teardown |

### 5.5 Desktop-only UX additions (later phase)

- Tray icon with session status + quick "ask lele" input.
- Global shortcut to summon a mini prompt window.
- Native notifications for approvals/subagent results (bridge WS events →
  `tauri-plugin-notification`).
- Auto-start at login (`tauri-plugin-autostart`).

## 6. Frontend Changes (web/)

Minimal and guarded so the same build keeps working in browser:

1. **Desktop bootstrap adapter** (~1 file):
   - `web/src/lib/desktop.ts`: detect `window.__LELE_DESKTOP__`; expose
     `isDesktop()`, `getDesktopSession()` (IPC invoke with typed fallback).
   - `AuthContext.tsx`: on mount, if `isDesktop()` and no stored session →
     hydrate session from IPC instead of showing PIN screen. Keep PIN screen
     reachable for "connect to another gateway".
2. **API URL resolution**: desktop mode forces `apiUrl` from bootstrap
   (ignore stored/`VITE_LELE_API_URL`).
3. **Disconnect overlay**: listen for sidecar-death event
   (`__LELE_DESKTOP__` event bridge) → overlay with "Restart backend" button
   calling `restart_backend`.
4. Optional cosmetics: hide "API URL" field in settings when desktop-local;
   add "Open data folder" button.

No changes to message rendering, WS client, or state logic.

## 7. Packaging & CI

### 7.1 Bundling

- `tauri.conf.json → bundle.externalBin`: `["binaries/lele"]` (Tauri appends
  the target triple automatically, e.g. `lele-x86_64-pc-windows-gnu.exe`).
- Build script cross-compiles Go for each triple with the same flags as
  goreleaser (`CGO_ENABLED=0 -tags stdjson`, ldflags version injection),
  then runs `bun run tauri build`.
- Targets v1:
  | OS | Triple | Artifact |
  |---|---|---|
  | macOS | aarch64-apple-darwin, x86_64-apple-darwin | .dmg (universal2 optional) |
  | Windows | x86_64-pc-windows-msvc | .msi / .exe (NSIS) |
  | Linux | x86_64-unknown-linux-gnu | .deb, .AppImage, .rpm |

### 7.2 CI (GitHub Actions)

- New workflow `desktop.yml`, matrix on `macos-latest`, `windows-latest`,
  `ubuntu-22.04`.
- Steps: setup-go + bun + rust toolchain → build sidecar → `tauri build` →
  upload artifacts. Release triggered by tag (can coexist with goreleaser
  release).
- Code signing / notarization: **phase 2** (document placeholders; macOS
  notarization needs Apple cert, Windows needs signtool/Azure). Unsigned
  builds fine for internal use.

### 7.3 Auto-update (phase 2)

- `tauri-plugin-updater` for the shell. Sidecar updates ship with the app
  (single bundle = atomic version match, no drift between UI/backend).

## 8. Security Considerations

- Sidecar binds **127.0.0.1 only** in desktop mode. No LAN exposure.
- Desktop token: 256-bit random, stored in OS keychain, passed via env (not
  argv — argv is visible to other users on some platforms), never logged.
- WebView origin = gateway origin ⇒ no CORS relaxation needed. Existing
  security headers middleware applies.
- Tauri v2 capabilities: deny-by-default; only enable the plugins listed in
  §5.4. No arbitrary shell access from the webview.
- `exec` tool risk unchanged (agent already runs local commands by design);
  desktop adds no new attack surface beyond localhost.

## 9. Phases & Deliverables

| Phase | Scope | Deliverable | Est. |
|---|---|---|---|
| **0. Spike** | Manual POC: Tauri dev window → existing `lele gateway` on 3005, verify WS, streaming, auth in WebView2/WebKit | Go/no-go + notes | 0.5 d |
| **1. Backend desktop mode** | §4: port-0, ready line, SIGTERM, desktop token, lockfile | `lele gateway --desktop` works headless; unit tests | 1-2 d |
| **2. Tauri shell core** | §5.1-5.3: sidecar lifecycle, bootstrap, window, loading splash | App launches sidecar and shows working UI, clean quit | 2-3 d |
| **3. Frontend adapter** | §6: desktop bootstrap, disconnect overlay | No PIN screen locally; reconnect UX | 1 d |
| **4. Packaging & CI** | §7: bundler config, icons, GH Actions matrix | Installers for 3 OSes from tag | 1-2 d |
| **5. Polish** | Tray, single-instance, autostart, notifications, updater | Beta-quality desktop app | 2-3 d |

Total: ~1.5–2 weeks of focused work.

## 10. Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| WebView engine differences (WebKitGTK on Linux) | Rendering/streaming bugs | Phase 0 spike on all 3 platforms; keep glamour/simple markdown paths tested |
| Zombie sidecar after crash | Port/DB contention | Lockfile + PID check on startup; `restart_backend` reaps by PID |
| User runs CLI `lele gateway` + desktop concurrently | Double Telegram polling, session contention | Lockfile detects; desktop offers "connect to running instance" (PIN flow) |
| Windows without WebView2 | App won't start | NSIS installer includes evergreen bootstrapper; document offline runtime option |
| Token leaks via process env | Local privilege boundary only | Acceptable: same user context; env over argv; keychain at rest |
| Port 0 refactor touches shared server code | Regression for headless | Keep `ListenAndServe` path default; listener path only when port=0; tests both |
| macOS app nap / background throttling of sidecar | Cron/heartbeat delays | Sidecar is a normal child process (not affected by WebView throttling); verify in soak test |

## 11. Open Questions (need decision)

1. **Close behavior default:** hide-to-tray (keeps agents running) vs quit.
   Recommendation: hide-to-tray with visible setting.
2. **Shared `~/.lele` state** between desktop and CLI installs: recommended YES
   (one source of truth), enforced by the lockfile. Alternative: separate
   `LELE_CONFIG_DIR` per app — rejected, fragments sessions.
3. **Repo location:** `desktop/` dir in the monorepo (recommended) vs separate
   repo. Monorepo keeps UI/backend/shell version-locked.
4. **App display name / bundle id:** e.g. `com.xilistudios.lele-desktop`.
5. **Universal macOS binary** vs per-arch dmg (size vs CI simplicity).

## 12. Test Strategy

- **Backend:** unit tests for port-0 listener, ready-line emission, desktop
  token validation, lockfile stale-PID recovery. Integration: spawn
  `--desktop` in test, assert ready JSON + health + token-authenticated API call.
- **Shell:** Rust integration test with a stub sidecar binary (echoes
  `LELE_READY`) validating spawn/parse/shutdown state machine.
- **Frontend:** existing suite + tests for desktop bootstrap branch
  (`isDesktop()` mocked), disconnect overlay.
- **Manual matrix:** smoke checklist per OS (launch, chat, streaming, tool
  approval, quit/restart, crash-recovery, upgrade over previous version).
