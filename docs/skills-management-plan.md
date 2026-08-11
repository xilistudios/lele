# Skills Management Plan — TUI `/skills` + WebUI Enhancement

## Overview

Implement a comprehensive skills management system that allows users to:
1. Browse and install skills from GitHub repos (single or multi-skill repos)
2. Select which skills to enable per workspace (`.lele/workspace.json`)
3. Manage skills from both TUI (`/skills` command) and WebUI (enhanced `SkillsPage`)

## Current State

### Backend (`pkg/skills/`)
- `installer.go`: `SkillInstaller.InstallFromGitHub(repo)` fetches ONE `SKILL.md` from `raw.githubusercontent.com/{repo}/main/SKILL.md`
- `loader.go`: `SkillsLoader` reads from 3 sources: workspace, global, builtin
- **Missing**: multi-skill repo scanning, workspace config persistence

### REST API (`pkg/channels/rest_system.go`)
- `GET /api/v1/skills` — list installed
- `POST /api/v1/skills` — install single skill from URL
- `GET /api/v1/skills/available` — list from `sipeed/lele-skills/skills.json`
- `DELETE /api/v1/skills/{name}` — remove
- **Missing**: scan repo endpoint, batch install, workspace config endpoints

### TUI (`pkg/tui/`)
- **No `/skills` command** — only modal types for sessions, agents, models, etc.
- Modal system exists with `modalType` enum, `renderModal()`, list/form patterns

### WebUI (`web/src/`)
- `SkillsPage.tsx` — full page with install modal (browse + URL tabs)
- `InstallSkillModal.tsx` — browse available OR enter GitHub URL
- **Missing**: multi-skill repo picker, workspace skill toggle

### Config
- `AgentConfig` has `Skills []string` field (per-agent, in config file)
- **Missing**: per-workspace skill enable/disable persistence

---

## Architecture

### Workspace Config

Skills selection is stored per-workspace in `{workspace}/.lele/workspace.json`:

```json
{
  "skills": {
    "enabled": ["github", "weather", "summarize"],
    "disabled": ["hardware"]
  }
}
```

- Skills in `enabled` are loaded into agent context
- Skills in `disabled` are installed but NOT loaded
- Skills not in either list default to **enabled** (backward compatible)
- The `SkillsLoader` checks this config when building context

### Multi-Skill Repo Discovery

When a user provides a GitHub repo URL like `user/repo`:
1. Use GitHub API (`GET /repos/{owner}/{repo}/contents/`) to list top-level dirs
2. For each dir, check if `{dir}/SKILL.md` exists via GitHub API or raw URL
3. Return list of discovered skills with names and descriptions
4. User selects which to install → batch install

**Alternative**: If repo has `skills.json` at root, use that as the manifest (faster, single request).

---

## Phase 1: Backend — Multi-Skill Repo Scanner

### 1.1 Add `ScanGitHubRepo` to `SkillInstaller`

**File**: `pkg/skills/installer.go`

```go
// ScannedSkill represents a skill found in a GitHub repo.
type ScannedSkill struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Path        string `json:"path"` // e.g. "weather" or "skills/weather"
    HasSKILL    bool   `json:"has_skill"`
}

// ScanGitHubRepo discovers skills in a GitHub repo by checking for
// SKILL.md files in subdirectories. Supports two layouts:
//   - Flat: repo/skill-name/SKILL.md
//   - Nested: repo/skills/skill-name/SKILL.md
func (si *SkillInstaller) ScanGitHubRepo(ctx context.Context, repo string) ([]ScannedSkill, error)
```

Implementation:
1. Try `GET https://api.github.com/repos/{owner}/{repo}/contents/` (no auth needed for public repos)
2. Parse response to find directories
3. For each directory, check if `SKILL.md` exists (parallel HEAD requests to raw.githubusercontent.com)
4. Also check `skills/` subdirectory for nested layout
5. For repos that ARE a single skill (root has SKILL.md), return single entry
6. Fetch first few lines of each SKILL.md to extract description from frontmatter

### 1.2 Add `InstallMultiple` to `SkillInstaller`

**File**: `pkg/skills/installer.go`

```go
// InstallMultiple installs selected skills from a scanned repo.
// skillPaths are the relative paths within the repo (e.g. ["weather", "github"]).
func (si *SkillInstaller) InstallMultiple(ctx context.Context, repo string, skillPaths []string) ([]string, error)
```

Implementation:
1. For each skill path, fetch `SKILL.md` from `raw.githubusercontent.com/{repo}/main/{path}/SKILL.md`
2. Write to `{workspace}/skills/{skillName}/SKILL.md`
3. Return list of successfully installed skill names

### 1.3 Workspace Config

**New file**: `pkg/skills/workspace_config.go`

```go
type WorkspaceSkillsConfig struct {
    Enabled  []string `json:"enabled,omitempty"`
    Disabled []string `json:"disabled,omitempty"`
}

// LoadWorkspaceConfig reads .lele/workspace.json from the workspace dir.
func LoadWorkspaceConfig(workspaceDir string) (*WorkspaceSkillsConfig, error)

// SaveWorkspaceConfig writes the config to .lele/workspace.json.
func SaveWorkspaceConfig(workspaceDir string, cfg *WorkspaceSkillsConfig) error

// IsEnabled checks if a skill should be loaded. Default: true.
func (c *WorkspaceSkillsConfig) IsEnabled(skillName string) bool

// SetEnabled marks a skill as enabled (removes from disabled).
func (c *WorkspaceSkillsConfig) SetEnabled(skillName string)

// SetDisabled marks a skill as disabled (removes from enabled).
func (c *WorkspaceSkillsConfig) SetDisabled(skillName string)
```

### 1.4 Update `SkillsLoader` to Respect Workspace Config

**File**: `pkg/skills/loader.go`

- Add `workspaceConfig *WorkspaceSkillsConfig` field
- In `ListSkills()`, mark each skill with `Enabled` bool based on config
- In `LoadSkillsForContext()`, skip disabled skills
- In `BuildSkillsSummary()`, skip disabled skills

### 1.5 New REST API Endpoints

**File**: `pkg/channels/rest_system.go`

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/skills/scan` | Scan a GitHub repo for skills. Body: `{"repo": "user/repo"}`. Returns `{"skills": [...], "repo": "user/repo"}` |
| `POST` | `/api/v1/skills/install-batch` | Install selected skills. Body: `{"repo": "user/repo", "skills": ["weather", "github"]}` |
| `PUT` | `/api/v1/skills/{name}/toggle` | Enable/disable a skill in workspace config. Body: `{"enabled": true/false}` |
| `GET` | `/api/v1/skills/workspace-config` | Get workspace skills config |

**File**: `pkg/channels/native.go` — register new routes

**File**: `pkg/channels/types.go` — add request/response types

---

## Phase 2: TUI — `/skills` Command

### 2.1 Add Modal Types

**File**: `pkg/tui/types.go`

```go
const (
    // ... existing modal types ...
    ModalSkills          // list of installed skills
    ModalSkillInstall    // scan/install from GitHub repo
    ModalSkillPicker     // multi-select which skills to install from scanned repo
)
```

New state fields:
```go
// Skills management state
skillsModalKeys      []string // maps modal items to skill names
skillsScanResults    []ScannedSkill // results from repo scan
skillsScanRepo       string   // repo being scanned
skillsSelectedMap    map[int]bool // multi-select state for skill picker
skillsFeedback       string   // brief feedback after install/remove
```

### 2.2 Add `/skills` to Commands

**File**: `pkg/tui/types.go` — add to `allCommands`:
```go
{name: "/skills", description: "Manage agent skills"},
```

### 2.3 Implement `/skills` Handler

**File**: `pkg/tui/commands.go`

The `/skills` command opens a modal with these actions:
```
┌─ Skills ─────────────────────────────────┐
│ ► 📋 Installed Skills (5)               │
│   📥 Install from GitHub                 │
│   🔧 Workspace Config                    │
│   ← Back                                 │
└──────────────────────────────────────────┘
```

**Sub-flows:**

**A. Installed Skills List:**
```
┌─ Installed Skills ───────────────────────┐
│ ● github      — Interact with GitHub...  │
│ ● weather     — Get weather forecasts... │
│ ● summarize   — Summarize URLs...        │
│ ○ hardware    — I2C/SPI peripherals...   │
│ ● tmux        — Remote tmux sessions...  │
│                                         │
│ [Enter] Toggle  [d] Delete  [i] Install  │
└──────────────────────────────────────────┘
```
- `Enter` toggles enabled/disabled (updates workspace config)
- `d` removes the skill (with confirmation)
- `i` switches to install flow

**B. Install from GitHub:**
```
┌─ Install Skill ──────────────────────────┐
│ GitHub repo or URL:                      │
│ ┌──────────────────────────────────────┐ │
│ │ user/repo                            │ │
│ └──────────────────────────────────────┘ │
│                                         │
│ Hint: user/repo or user/repo/skill-name │
│                                         │
│ [Enter] Scan  [Esc] Back                │
└──────────────────────────────────────────┘
```

After scanning, if multiple skills found:
```
┌─ Select Skills to Install ──────────────┐
│ Repo: sipeed/lele-skills                │
│                                         │
│ [x] weather    — Weather forecasts      │
│ [x] github     — GitHub integration     │
│ [ ] summarize  — URL summarization      │
│ [x] tmux       — Tmux control           │
│                                         │
│ [Space] Toggle  [Enter] Install  [Esc]  │
└─────────────────────────────────────────┘
```

### 2.4 Modal Rendering

**File**: `pkg/tui/view.go`

- Add `ModalSkills`, `ModalSkillInstall`, `ModalSkillPicker` to modal title switch
- `ModalSkillInstall` uses `renderFormModal` (text input for repo URL)
- `ModalSkillPicker` uses a custom multi-select renderer (checkbox-style)
- `ModalSkills` uses `renderModal` with action items

### 2.5 Key Handling

**File**: `pkg/tui/handlers.go`

- Add `ModalSkills` to `isListModal()` (returns true)
- Handle `Enter` on `ModalSkills` items:
  - "Installed Skills" → switch to installed list
  - "Install from GitHub" → switch to `ModalSkillInstall`
  - "Workspace Config" → switch to workspace config view
- Handle `ModalSkillInstall` text input + Enter → spawn scan, show `ModalSkillPicker`
- Handle `ModalSkillPicker`:
  - `Space` toggles checkbox
  - `Enter` installs selected skills
  - Up/down navigation
- Handle installed list:
  - `Enter` toggles enable/disable
  - `d` deletes with confirmation

---

## Phase 3: WebUI — Multi-Skill Install + Workspace Toggle

### 3.1 Enhance `InstallSkillModal`

**File**: `web/src/components/organisms/InstallSkillModal.tsx`

When user enters a repo URL and clicks "Scan":
1. Call `POST /api/v1/skills/scan` with the repo
2. Show discovered skills with checkboxes
3. User selects which to install
4. Call `POST /api/v1/skills/install-batch`

New state:
```typescript
const [scannedSkills, setScannedSkills] = useState<ScannedSkill[]>([])
const [selectedSkills, setSelectedSkills] = useState<Set<string>>(new Set())
const [isScanning, setIsScanning] = useState(false)
const [scanRepo, setScanRepo] = useState('')
```

New UI flow in "Install from URL" tab:
1. User enters `user/repo` → clicks "Scan"
2. Shows loading spinner
3. Shows skill list with checkboxes + "Install Selected" button
4. If only 1 skill found, auto-select and install directly

### 3.2 Add Skill Toggle to `SkillsList`

**File**: `web/src/components/organisms/SkillsList.tsx`

Add an enable/disable toggle to each skill card:
- Toggle switch (on/off) for workspace-local skills
- Calls `PUT /api/v1/skills/{name}/toggle`
- Shows "Disabled" badge for disabled skills

### 3.3 Update `useSkills` Hook

**File**: `web/src/hooks/useSkills.ts`

Add new functions:
```typescript
scanRepo: (repo: string) => Promise<ScannedSkill[]>
installBatch: (repo: string, skills: string[]) => Promise<void>
toggleSkill: (name: string, enabled: boolean) => Promise<void>
```

### 3.4 Update API Client

**File**: `web/src/services/http/client.ts`

```typescript
scanSkills: (repo: string) => request<ScanSkillsResponse>(endpoints.skills.scan, {
  method: 'POST',
  body: JSON.stringify({ repo }),
}),
installSkillsBatch: (repo: string, skills: string[]) => request(endpoints.skills.installBatch, {
  method: 'POST',
  body: JSON.stringify({ repo, skills }),
}),
toggleSkill: (name: string, enabled: boolean) => request(endpoints.skills.toggle(name), {
  method: 'PUT',
  body: JSON.stringify({ enabled }),
}),
```

### 3.5 Update Endpoints

**File**: `web/src/services/http/endpoints.ts`

```typescript
skills: {
  // ... existing ...
  scan: '/api/v1/skills/scan',
  installBatch: '/api/v1/skills/install-batch',
  toggle: (name: string) => `/api/v1/skills/${encodeURIComponent(name)}/toggle`,
  workspaceConfig: '/api/v1/skills/workspace-config',
},
```

### 3.6 Update Types

**File**: `web/src/lib/types.ts`

```typescript
export type ScannedSkill = {
  name: string
  description: string
  path: string
  has_skill: boolean
}

export type ScanSkillsResponse = {
  skills: ScannedSkill[]
  repo: string
}

export type SkillToggleRequest = {
  enabled: boolean
}
```

---

## Phase 4: Tests

### 4.1 Backend Tests

**File**: `pkg/skills/installer_test.go` (new)
- `TestScanGitHubRepo_SingleSkill` — repo with SKILL.md at root
- `TestScanGitHubRepo_MultiSkill` — repo with skills/*/SKILL.md
- `TestScanGitHubRepo_NestedLayout` — repo with skills/skills/*/SKILL.md
- `TestScanGitHubRepo_Empty` — repo with no skills
- `TestInstallMultiple_SelectedSkills` — install subset
- `TestInstallMultiple_AlreadyExists` — skip existing

**File**: `pkg/skills/workspace_config_test.go` (new)
- `TestLoadWorkspaceConfig_Exists` — reads valid config
- `TestLoadWorkspaceConfig_Missing` — returns empty config
- `TestSaveWorkspaceConfig_CreateDir` — creates .lele/ if needed
- `TestIsEnabled_Default` — unknown skills default to enabled
- `TestSetEnabled_Disabled` — moves from disabled to enabled
- `TestSetDisabled_Enabled` — moves from enabled to disabled

**File**: `pkg/channels/rest_system_test.go` (extend)
- `TestHandleSkillScan` — mock GitHub API, verify response
- `TestHandleSkillInstallBatch` — install multiple, verify files
- `TestHandleSkillToggle` — toggle enabled/disabled

### 4.2 TUI Tests

**File**: `pkg/tui/skills_test.go` (new)
- `TestSkillsCommand_OpensModal` — /skills opens ModalSkills
- `TestSkillsModal_NavigateItems` — up/down navigation
- `TestSkillPicker_ToggleSelection` — space toggles checkbox
- `TestSkillInstall_SubmitRepo` — enter submits repo URL
- `TestSkillToggle_WorkspaceConfig` — toggle updates config

---

## File Summary

### New Files
| File | Purpose |
|------|---------|
| `pkg/skills/workspace_config.go` | Workspace skills config persistence |
| `pkg/skills/workspace_config_test.go` | Tests |
| `pkg/skills/scanner.go` | GitHub repo scanning logic |
| `pkg/skills/scanner_test.go` | Tests |
| `pkg/tui/skills.go` | TUI skills modal handlers |
| `pkg/tui/skills_test.go` | Tests |

### Modified Files
| File | Changes |
|------|---------|
| `pkg/skills/installer.go` | Add `InstallMultiple` method |
| `pkg/skills/loader.go` | Respect workspace config for enabled/disabled |
| `pkg/channels/rest_system.go` | Add scan, install-batch, toggle endpoints |
| `pkg/channels/native.go` | Register new routes |
| `pkg/channels/types.go` | Add request/response types |
| `pkg/tui/types.go` | Add modal types + state fields |
| `pkg/tui/commands.go` | Add `/skills` command handler |
| `pkg/tui/handlers.go` | Add key handling for new modals |
| `pkg/tui/view.go` | Add rendering for new modals |
| `web/src/components/organisms/InstallSkillModal.tsx` | Multi-skill scan + picker |
| `web/src/components/organisms/SkillsList.tsx` | Add toggle switch |
| `web/src/hooks/useSkills.ts` | Add scan, batch install, toggle |
| `web/src/services/http/client.ts` | Add new API methods |
| `web/src/services/http/endpoints.ts` | Add new endpoints |
| `web/src/lib/types.ts` | Add new types |

---

## Implementation Order

1. **Phase 1.3** — Workspace config (no deps, foundation for everything)
2. **Phase 1.1** — Repo scanner (GitHub API client)
3. **Phase 1.2** — Batch installer
4. **Phase 1.4** — Update loader to respect config
5. **Phase 1.5** — REST API endpoints
6. **Phase 2** — TUI `/skills` command (depends on Phase 1)
7. **Phase 3** — WebUI enhancements (depends on Phase 1)
8. **Phase 4** — Tests (throughout, but final pass here)

---

## Risk Mitigation

- **GitHub API rate limits**: Use raw.githubusercontent.com for SKILL.md checks (no rate limit). Only use API for directory listing.
- **Large repos**: Limit scan to top-level + `skills/` subdir. Don't recurse deeply.
- **Network errors**: All GitHub calls have 15s timeout. Graceful fallback to URL-only install.
- **Backward compatibility**: Skills not in workspace config default to enabled. Existing behavior unchanged.
- **Auth for private repos**: Future enhancement. Current implementation works for public repos only.
