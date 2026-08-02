# Keyring Module — Implementation Plan

> Status: DRAFT v2
> Author: lele 🦞
> Date: 2026-08-01

## 1. Overview

The keyring module provides encrypted secret storage that agents can reference
by name without ever seeing raw values in their LLM context. Secrets are
managed via TUI (`/secrets`) and WebUI (dedicated panel), and consumed by
agents through a scoped tool that injects values at execution time.

### Design Principles

- **Zero-friction**: Uses the OS keychain by default. No prompts, no passphrases.
  The user unlocks their session once (login), and lele inherits access.
- **Zero-leak**: Secret values never appear in LLM prompts, logs, or session history.
- **Encrypted at rest**: AES-256-GCM. Master key lives in the OS keychain
  (macOS Keychain, GNOME Keyring/KWallet, Windows Credential Manager).
- **Audit trail**: Every secret access is logged with agent ID, timestamp, and purpose.
- **Scoped access**: Agents can be restricted to specific secret namespaces.
- **Graceful fallback**: On systems without a keychain (headless servers, containers),
  falls back to a local key file (`~/.lele/keyring.key`, perms 0600).

---

## 2. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                        Interfaces                            │
│  ┌──────────┐  ┌──────────┐  ┌────────────────────────────┐  │
│  │ TUI      │  │ WebUI    │  │ Agent Tool                 │  │
│  │ /secrets │  │ /secrets │  │ secret (list/get)          │  │
│  └────┬─────┘  └────┬─────┘  └──────────┬─────────────────┘  │
│       │              │                   │                    │
│       ▼              ▼                   ▼                    │
│  ┌──────────────────────────────────────────────────────┐    │
│  │                keyring.Service                        │    │
│  │  (business logic, access control, audit log)         │    │
│  └───────────────────────┬──────────────────────────────┘    │
│                          │                                    │
│                          ▼                                    │
│  ┌──────────────────────────────────────────────────────┐    │
│  │                keyring.Store                          │    │
│  │  (encrypted vault: AES-256-GCM → ~/.lele/keyring.enc)│    │
│  └───────────────────────┬──────────────────────────────┘    │
│                          │                                    │
│                          ▼                                    │
│  ┌──────────────────────────────────────────────────────┐    │
│  │              keyring.KeyProvider                       │    │
│  │                                                       │    │
│  │  ┌─────────────────┐    ┌──────────────────────────┐  │    │
│  │  │ OS Keychain     │    │ Fallback: key file       │  │    │
│  │  │ (primary)       │    │ ~/.lele/keyring.key      │  │    │
│  │  │                 │    │ (perms 0600)             │  │    │
│  │  │ macOS Keychain  │    │                          │  │    │
│  │  │ GNOME Keyring   │    │ Used when:               │  │    │
│  │  │ KWallet         │    │  - no keychain detected  │  │    │
│  │  │ Win Credential  │    │  - LELE_KEYRING_BACKEND  │  │    │
│  │  │ Manager         │    │    = "file"              │  │    │
│  │  └─────────────────┘    └──────────────────────────┘  │    │
│  └──────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────┘
```

### Unlock Flow (zero-friction)

```
lele starts
    │
    ▼
KeyProvider.GetKey()
    │
    ├─ Try OS keychain (zalando/go-keyring)
    │   ├─ Found "lele/keyring-master-key" → return key ✅ (no prompt)
    │   └─ Not found → generate 32-byte random key
    │       → store in OS keychain → return key ✅
    │
    └─ Keychain unavailable (headless/container)
        ├─ ~/.lele/keyring.key exists → read → return key ✅
        └─ Not found → generate 32-byte random key
            → write to ~/.lele/keyring.key (0600) → return key ✅
```

**The user never types a passphrase in the normal flow.**

---

## 3. Package: `pkg/keyring/`

### 3.1 Data Model (`model.go`)

```go
// Secret represents a stored secret entry.
type Secret struct {
    Name        string    `json:"name"`         // unique key, e.g. "openai.api_key"
    Description string    `json:"description"`  // human-readable purpose
    Value       string    `json:"-"`            // NEVER serialized to JSON responses
    Tags        []string  `json:"tags"`         // e.g. ["provider", "openai"]
    Scope       []string  `json:"scope"`        // agent IDs allowed (empty = all)
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
    CreatedBy   string    `json:"created_by"`   // "tui", "webui", "agent:coder"
}

// SecretMeta is the safe representation (no value) for listing.
type SecretMeta struct {
    Name        string    `json:"name"`
    Description string    `json:"description"`
    Tags        []string  `json:"tags"`
    Scope       []string  `json:"scope"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
    CreatedBy   string    `json:"created_by"`
}

// AccessRecord is an audit log entry.
type AccessRecord struct {
    SecretName string    `json:"secret_name"`
    AgentID    string    `json:"agent_id"`
    SessionKey string    `json:"session_key"`
    Action     string    `json:"action"` // "get", "set", "delete", "list"
    Timestamp  time.Time `json:"timestamp"`
    Granted    bool      `json:"granted"`
}
```

### 3.2 KeyProvider (`keyprovider.go`)

```go
// KeyProvider retrieves or creates the master encryption key.
type KeyProvider interface {
    // GetKey returns the 32-byte master key, creating it if necessary.
    GetKey() ([]byte, error)
    // Backend returns the active backend name ("keychain" or "file").
    Backend() string
    // Available reports whether this provider can function.
    Available() bool
}

// OSKeychainProvider stores the master key in the OS keychain.
type OSKeychainProvider struct {
    service string // "lele"
    account string // "keyring-master-key"
}

func (p *OSKeychainProvider) GetKey() ([]byte, error) {
    // 1. Try keyring.Get("lele", "keyring-master-key")
    // 2. If not found → crypto/rand 32 bytes → keyring.Set(...)
    // 3. Return key
}

func (p *OSKeychainProvider) Available() bool {
    // Probe: try keyring.Get with a test read.
    // Returns false on: no D-Bus (headless Linux), no keychain binary, etc.
}

// FileKeyProvider stores the master key in a local file (fallback).
type FileKeyProvider struct {
    path string // ~/.lele/keyring.key
}

func (p *FileKeyProvider) GetKey() ([]byte, error) {
    // 1. If file exists → read 32 bytes
    // 2. If not → generate → write with 0600
    // 3. Return key
}

// NewKeyProvider returns the best available provider.
func NewKeyProvider(leleDir string) KeyProvider {
    // Respect explicit override
    if os.Getenv("LELE_KEYRING_BACKEND") == "file" {
        return &FileKeyProvider{path: filepath.Join(leleDir, "keyring.key")}
    }
    // Try OS keychain
    osProvider := &OSKeychainProvider{service: "lele", account: "keyring-master-key"}
    if osProvider.Available() {
        return osProvider
    }
    // Fallback
    return &FileKeyProvider{path: filepath.Join(leleDir, "keyring.key")}
}
```

### 3.3 Store (`store.go` + `file_store.go`)

```go
// Store is the persistence backend for secrets.
type Store interface {
    // Open decrypts the vault file using the provided key.
    Open(key []byte) error
    // Close flushes and zeroes the in-memory key.
    Close() error
    // IsOpen returns true if the vault is decrypted and ready.
    IsOpen() bool
    // Set stores or updates a secret.
    Set(secret *Secret) error
    // Get retrieves a secret by name (returns value).
    Get(name string) (*Secret, error)
    // Delete removes a secret.
    Delete(name string) error
    // List returns metadata for all secrets (no values).
    List() ([]SecretMeta, error)
    // Search finds secrets matching a query (name, tags, description).
    Search(query string) ([]SecretMeta, error)
    // Flush persists changes to disk (re-encrypts).
    Flush() error
}
```

**File format** (`~/.lele/keyring.enc`):
```
[12-byte nonce][ciphertext (JSON vault)][16-byte GCM tag]
```

- No salt needed (key comes from keychain/file, not derived from passphrase).
- Vault JSON structure: `{"version": 1, "secrets": [...]}`
- On `Open`: decrypt → unmarshal → hold in memory.
- On `Flush`: marshal → encrypt → atomic write (write tmp + rename).
- On `Close`: zero the key slice, nil the secrets map.

### 3.4 Service Layer (`service.go`)

```go
type Service struct {
    store      Store
    keyProv    KeyProvider
    auditLog   *AuditRing  // ring buffer (configurable size, default 1000)
    mu         sync.RWMutex
    openedOnce sync.Once
}

// EnsureOpen lazily opens the store on first access.
func (s *Service) EnsureOpen() error {
    var err error
    s.openedOnce.Do(func() {
        key, e := s.keyProv.GetKey()
        if e != nil { err = e; return }
        err = s.store.Open(key)
    })
    return err
}

func (s *Service) GetForAgent(name, agentID, sessionKey string) (string, error)
func (s *Service) SetFromUI(name, value, description string, tags, scope []string, source string) error
func (s *Service) DeleteFromUI(name, source string) error
func (s *Service) ListForAgent(agentID string) ([]SecretMeta, error)
func (s *Service) ListAll() ([]SecretMeta, error)
func (s *Service) Search(query string) ([]SecretMeta, error)
func (s *Service) AuditLog() []AccessRecord
func (s *Service) Backend() string  // "keychain" or "file" (for UI display)
```

**Access control in `GetForAgent`:**
1. `EnsureOpen()` — lazy unlock (transparent, no user interaction).
2. Check if secret exists → error if not.
3. Check scope: if `secret.Scope` is non-empty, `agentID` must be in the list.
4. Log the access (granted or denied).
5. Return the raw value.

---

## 4. Agent Tool: `pkg/tools/secret.go`

### 4.1 Tool Definition

```json
{
  "name": "secret",
  "description": "Access stored secrets by name. Use 'list' to see available secrets, 'get' to retrieve a value for use in API calls or commands. Values are injected securely and never shown in chat.",
  "parameters": {
    "type": "object",
    "properties": {
      "action": {
        "type": "string",
        "enum": ["list", "get"],
        "description": "Action to perform"
      },
      "name": {
        "type": "string",
        "description": "Secret name (required for 'get')"
      }
    },
    "required": ["action"]
  }
}
```

### 4.2 Execution Behavior

- **`list`**: Returns secret names + descriptions (no values). Filtered by agent scope.
- **`get`**: Returns the value to the agent tool result. The value IS visible to the LLM
  in the tool result (necessary for the agent to use it in API calls, curl commands, etc.),
  BUT:
  - The tool result is marked `sensitive: true` so the TUI/WebUI can mask it in display.
  - Audit log records every access.
  - Optional: a `mask_in_history` flag can redact the value from session history after use.

### 4.3 Alternative: Injection-Only Mode (phase 6, stretch)

- Agent calls `secret(action="get", name="openai.api_key")` → returns `{{SECRET:openai.api_key}}`
- When the agent uses the placeholder in `exec` or `web_fetch`, the tool executor
  substitutes the real value at execution time.
- The LLM never sees the actual value.

---

## 5. TUI: `/secrets` Command

### 5.1 Command Registration

Add to `allCommands` in `pkg/tui/types.go`:
```go
{name: "/secrets", description: "Manage secrets (keyring)"},
```

### 5.2 Modal Flow

New modal type: `ModalSecrets`

**List view** (default):
```
🔐 Secrets (3 stored) [keychain: GNOME Keyring]
─────────────────────────────────────────────────
  openai.api_key     [provider]   2026-07-15
  github.token       [devops]     2026-07-20
  slack.webhook_url  [notify]     2026-07-28
─────────────────────────────────────────────────
[a] Add  [d] Delete  [v] View  [ESC] Back
```

Note: No unlock prompt. The keychain is already unlocked by the OS session.
If running with file fallback, show `[file: ~/.lele/keyring.key]` in the header.

**Add flow** (multi-step form, similar to `/connect`):
1. Name (e.g. `openai.api_key`)
2. Value (password input, masked)
3. Description (optional)
4. Tags (comma-separated, optional)
5. Scope (agent IDs, comma-separated, empty = all)
6. Confirm → save

**View**: Shows metadata + masked value (`sk-****...****abc`). Press `r` to reveal temporarily.

**Delete**: Confirmation prompt before deletion.

### 5.3 Edge Case: Keychain Locked

On Linux, if the user's keyring is locked (rare — happens if auto-unlock on login
is disabled), the OS will show its own unlock dialog (GNOME/KDE handles this).
Lele does NOT need its own passphrase prompt. The `go-keyring` library blocks
until the OS keyring is unlocked.

If the keychain is completely unavailable (no D-Bus, no keyring daemon):
- Lele falls back to file backend automatically.
- TUI shows `[file fallback]` indicator.
- No user interaction needed.

### 5.4 Implementation Files

- `pkg/tui/commands.go` — add `/secrets` case
- `pkg/tui/handlers.go` — key handling for ModalSecrets
- `pkg/tui/view.go` — render secrets modal
- `pkg/tui/types.go` — add `ModalSecrets` constant + state fields

---

## 6. WebUI: Secrets Panel

### 6.1 API Endpoints

Register on the unified server (`pkg/server/`):

```
GET    /api/secrets              → list all secrets (metadata only)
POST   /api/secrets              → create/update a secret
DELETE /api/secrets/{name}       → delete a secret
GET    /api/secrets/{name}       → get secret value (for "reveal" in UI)
GET    /api/secrets/audit        → access audit log
GET    /api/secrets/status       → backend info ("keychain"/"file"), secret count
```

All endpoints require the existing native channel auth token.
No unlock endpoint needed — the keyring is always open (keychain or file).

### 6.2 Frontend Components (`web/src/`)

```
web/src/
├── components/
│   └── secrets/
│       ├── SecretsPanel.tsx      — main container (list + actions)
│       ├── SecretListItem.tsx    — single secret row
│       ├── SecretForm.tsx        — add/edit form (slide-over)
│       ├── SecretDetail.tsx      — detail view with reveal toggle
│       └── AuditLog.tsx          — access history table
├── services/
│   └── secrets.ts               — API client functions
└── hooks/
    └── useSecrets.ts            — React hook for secrets state
```

### 6.3 UI Design

- Sidebar navigation item: "🔐 Secrets" (between Settings and Logs)
- List view: table with Name, Description, Tags, Scope, Updated, Actions
- Add/Edit: slide-over panel with form fields
- Value input: password field with show/hide toggle
- Reveal: click eye icon → shows value for 10 seconds → auto-hides
- Audit tab: chronological log of who accessed what
- Status bar: shows backend type ("GNOME Keyring" / "file fallback")

---

## 7. Configuration

Add to `config.json`:

```json
{
  "keyring": {
    "enabled": true,
    "path": "~/.lele/keyring.enc",
    "backend": "auto",
    "audit_log_size": 1000,
    "allow_agent_set": false,
    "allow_agent_delete": false
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `true` | Enable/disable the keyring module |
| `path` | `~/.lele/keyring.enc` | Vault file location |
| `backend` | `"auto"` | `"auto"` (keychain→file), `"keychain"`, or `"file"` |
| `audit_log_size` | `1000` | Max audit records in memory |
| `allow_agent_set` | `false` | Allow agents to store secrets via tool |
| `allow_agent_delete` | `false` | Allow agents to delete secrets via tool |

Config struct in `pkg/config/config.go`:
```go
type KeyringConfig struct {
    Enabled          bool   `json:"enabled" env:"LELE_KEYRING_ENABLED"`
    Path             string `json:"path" env:"LELE_KEYRING_PATH"`
    Backend          string `json:"backend" env:"LELE_KEYRING_BACKEND"` // "auto", "keychain", "file"
    AuditLogSize     int    `json:"audit_log_size" env:"LELE_KEYRING_AUDIT_LOG_SIZE"`
    AllowAgentSet    bool   `json:"allow_agent_set" env:"LELE_KEYRING_ALLOW_AGENT_SET"`
    AllowAgentDelete bool   `json:"allow_agent_delete" env:"LELE_KEYRING_ALLOW_AGENT_DELETE"`
}
```

---

## 8. Integration Points

### 8.1 Agent Loop (`pkg/agent/`)

- `tool_coordinator.go`: Register the `secret` tool during tool setup.
- Pass `agentID` and `sessionKey` to the tool via context (already available via `WithToolContext`).
- The `keyring.Service` instance lives on `AgentLoop` and is passed to the tool.

### 8.2 Config Integration (`pkg/config/document_secret.go`)

- Existing `{{ENV_*}}` placeholders continue to work.
- New: `{{SECRET:name}}` placeholder syntax for config values.
  - Example: `"api_key": "{{SECRET:openai.api_key}}"` in config.json.
  - Resolved at config load time by the keyring service.
  - This replaces the need to put API keys in env vars or plaintext.

### 8.3 Server Startup (`cmd/lele/`)

- Initialize `keyring.Service` during startup (lazy — no I/O until first access).
- Register API routes on the server mux.
- No unlock step. The KeyProvider handles key retrieval transparently.

### 8.4 Session History Sanitization

- Tool results from `secret get` are tagged with `sensitive: true`.
- `pkg/agent/context.go` (sanitization): optionally redact sensitive tool results
  from history after N turns (configurable).
- TUI/WebUI display: mask sensitive values in rendered messages.

---

## 9. Security Considerations

| Threat | Mitigation |
|--------|-----------|
| Disk theft (powered off) | OS keychain encrypts master key; vault is AES-256-GCM |
| Disk theft (running) | Key in RAM only; OS keychain may require login to access |
| Another local user | Keychain is per-user; file fallback uses 0600 perms |
| Malware with same user | Same trust boundary as any app (browser passwords face same risk) |
| LLM exfiltration | Audit log, scope restrictions, optional injection-only mode |
| Unauthorized agent | Scope field per secret, deny by default for scoped secrets |
| Container/headless leak | File fallback: key + vault are separate files; mount secrets externally |
| Log leakage | Secret values never logged; only names in audit |

### Threat model comparison

| | OS Keychain | File fallback | Passphrase (removed) |
|---|---|---|---|
| Protects against disk theft | ✅ (key encrypted by OS) | ⚠️ (both files on disk) | ✅ |
| Protects against other users | ✅ | ✅ (0600) | ✅ |
| Zero user interaction | ✅ | ✅ | ❌ |
| Works headless | ❌ (needs D-Bus) | ✅ | ⚠️ (needs env var) |
| Protects against root | ❌ | ❌ | ⚠️ (argon2id slows brute) |

The OS keychain is the right default for a personal assistant: same security
as your browser's saved passwords, with zero friction.

---

## 10. Implementation Phases

### Phase 1: Core (1-2 days)
- [ ] `pkg/keyring/model.go` — types
- [ ] `pkg/keyring/keyprovider.go` — OSKeychainProvider + FileKeyProvider + NewKeyProvider
- [ ] `pkg/keyring/store.go` — Store interface
- [ ] `pkg/keyring/file_store.go` — AES-256-GCM encrypted vault
- [ ] `pkg/keyring/service.go` — Service with access control + lazy open
- [ ] `pkg/keyring/audit.go` — ring buffer
- [ ] `pkg/keyring/keyring_test.go` — unit tests (mock keychain + file backend)
- [ ] `pkg/config/config.go` — KeyringConfig struct + defaults
- [ ] Dependency: `github.com/zalando/go-keyring`

### Phase 2: Agent Tool (0.5 day)
- [ ] `pkg/tools/secret.go` — list + get actions
- [ ] Register in tool coordinator
- [ ] Context propagation (agentID, sessionKey)
- [ ] Test: agent can list and retrieve secrets

### Phase 3: TUI `/secrets` (1 day)
- [ ] Modal type + command registration
- [ ] List view with keyboard navigation
- [ ] Add/Edit/Delete flows (multi-step form)
- [ ] Backend indicator in header
- [ ] i18n strings (es/en/pt)

### Phase 4: WebUI Panel (1-2 days)
- [ ] API endpoints on server
- [ ] React components (list, form, detail, audit)
- [ ] Integration with existing sidebar navigation
- [ ] Status bar showing backend type

### Phase 5: Config Integration (0.5 day)
- [ ] `{{SECRET:name}}` placeholder resolution in `document_secret.go`
- [ ] Migration guide: move existing API keys to keyring
- [ ] Documentation update

### Phase 6: Hardening (stretch)
- [ ] Injection-only mode (placeholder substitution in exec/web_fetch)
- [ ] Secret rotation reminders
- [ ] Import/export (encrypted backup)
- [ ] Per-agent keyrings (multi-tenant)

---

## 11. File Manifest

```
pkg/keyring/
├── model.go            — Secret, SecretMeta, AccessRecord types
├── keyprovider.go      — KeyProvider interface, OSKeychainProvider, FileKeyProvider
├── store.go            — Store interface
├── file_store.go       — AES-256-GCM encrypted vault backend
├── service.go          — Service (access control, lazy open, CRUD)
├── audit.go            — AuditRing (ring buffer)
├── keyring_test.go     — Unit tests
└── file_store_test.go  — Store tests

pkg/tools/
└── secret.go           — Agent tool (list/get)

pkg/tui/
├── commands.go         — /secrets case (modify)
├── handlers.go         — ModalSecrets key handling (modify)
├── view.go             — Secrets modal rendering (modify)
└── types.go            — ModalSecrets constant + state (modify)

pkg/server/
└── server.go           — Register /api/secrets routes (modify)

pkg/config/
├── config.go           — KeyringConfig struct (modify)
└── document_secret.go  — {{SECRET:}} resolution (modify)

web/src/
├── components/secrets/ — New React components
├── services/secrets.ts — API client
└── hooks/useSecrets.ts — State hook

cmd/lele/
└── main.go             — Keyring service init (modify)
```

---

## 12. Example Usage

### First-time setup (automatic, no user action):
```
$ lele tui
# Keyring initializes silently:
#   1. Detects GNOME Keyring via D-Bus
#   2. Generates master key, stores in keychain
#   3. Creates empty ~/.lele/keyring.enc
# User sees nothing. It just works.
```

### Agent uses a secret:
```
User: "Check my GitHub notifications"

Agent thinks: I need the GitHub token.
Agent calls: secret(action="get", name="github.token")
Tool result: "ghp_xxxxxxxxxxxx"  [sensitive]

Agent calls: exec(command="curl -H 'Authorization: token ghp_xxxx' https://api.github.com/notifications")
Tool result: [notifications JSON]

Agent responds: "You have 3 unread notifications..."
```

### TUI management:
```
> /secrets
🔐 Secrets (3 stored) [GNOME Keyring]
  1. openai.api_key     [provider]     Updated 2026-07-15
  2. github.token       [devops]       Updated 2026-07-20
  3. slack.webhook_url  [notify]       Updated 2026-07-28

[a] Add  [d] Delete  [v] View  [ESC] Back
```

### Config placeholder:
```json
{
  "providers": {
    "openai": {
      "api_key": "{{SECRET:openai.api_key}}"
    }
  }
}
```

### Headless server (no keychain):
```
$ lele serve
# Keyring detects no D-Bus/keychain
# Falls back to file backend:
#   ~/.lele/keyring.key (0600) + ~/.lele/keyring.enc (0600)
# Logs: "keyring: using file backend (no OS keychain detected)"
```

---

## 13. Dependencies

New Go dependencies:
- `github.com/zalando/go-keyring` — OS keychain abstraction (macOS/Linux/Windows)
- `golang.org/x/crypto` — likely already present; needed for any crypto utils

`zalando/go-keyring` backends:
- macOS: Security.framework (via cgo or exec `security` CLI)
- Linux: D-Bus → org.freedesktop.secrets (GNOME Keyring, KWallet)
- Windows: Windows Credential Manager (via `wincred`)

No new frontend dependencies (uses existing React + Tailwind stack).

---

## 14. Open Questions

1. **Should `secret get` return the value to the LLM?**
   - Phase 1: Yes (simpler, agent needs it for curl/exec).
   - Phase 6: Optional injection-only mode for high-security setups.

2. **Multi-user support?**
   - Phase 1: Single keyring per lele instance.
   - Future: Per-agent keyrings or per-channel scoping.

3. **Secret versioning?**
   - Phase 1: No (overwrite on update).
   - Future: Keep last N versions for rollback.

4. **Key rotation?**
   - Phase 1: No. Master key is permanent (stored in keychain).
   - Future: `lele keyring rotate-key` command that re-encrypts the vault
     with a new key and updates the keychain entry.

5. **What if the user clears their OS keychain?**
   - The master key is lost → vault is unrecoverable.
   - On next access, lele detects decryption failure → logs error →
     offers to reinitialize (creates new key + empty vault).
   - User must re-add secrets. Document this clearly.
