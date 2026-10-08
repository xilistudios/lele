/**
 * MCP management types — a 1:1 mirror of the backend contract
 * (pkg/channels/rest_mcp.go).
 *
 * Optionality follows Go's `omitempty`: a field marked omitempty is ABSENT
 * when empty, so it is typed optional here (absent ≈ empty/false/0). Fields
 * without omitempty (e.g. `name`, `exists`, `content`, `defines`) are always
 * present and stay required.
 *
 * No secret VALUES ever cross these routes: `env_keys`/`header_keys` are
 * NAMES only and the raw `content` is the literal file bytes with `${VAR}`
 * never expanded. Nothing in here should ever grow a value-shaped field.
 */

/** Stored layers of the mcp.json stack: global | agent | project. */
export type MCPLayerName = 'global' | 'agent' | 'project'

/**
 * Layer as accepted by the toggle route: the three stored layers plus
 * `auto` (the server resolves the layer owning the winning copy). The raw
 * routes refuse `auto` (invalid_layer).
 */
export type MCPQueryLayer = MCPLayerName | 'auto'

/** Inventory verdict for a name. `invalid` is still a toggleable entry. */
export type MCPEffective = 'enabled' | 'disabled' | 'invalid'

/** Display-safe slice of one server: raw strings and key NAMES only. */
export interface MCPServerSummary {
  kind?: string
  command?: string
  url?: string
  type?: string
  description?: string
  /** Why the entry fails to parse/validate (row.effective === 'invalid'). */
  invalid?: string
  /** Number of args (0 omitted), not the arg list. */
  args?: number
  disabled?: boolean
  env_keys?: string[]
  header_keys?: string[]
}

/** One INERT copy of a name below the winner (a name appears exactly once). */
export interface MCPShadow {
  layer: string
  path?: string
  /** "shadowed by <winning layer>" */
  reason: string
}

/** Winning copy of one server NAME. */
export interface MCPServerRow {
  name: string
  /** Layer owning the winning copy — use this (never 'auto') for writes. */
  layer: string
  path?: string
  effective: MCPEffective
  /** The winning copy actually defines the server (vs. a disable-only entry). */
  defines: boolean
  /** The inert copies below the winner; absent when the name has none. */
  shadowed?: MCPShadow[]
  server: MCPServerSummary
}

/** One mcp.json layer (low → high; aliases folded). */
export interface MCPLayer {
  layer: string
  /** Absent ⇔ the layer is DISABLED (no workspace root) — never read/write it. */
  path?: string
  exists: boolean
  /** Other layers resolving to the SAME file (aliased roots), low → high. */
  aliased_with?: string[]
}

/** GET /api/v1/mcp?agent_id= — merged inventory, one row per NAME. */
export interface MCPInventoryResponse {
  agent_id: string
  layers?: MCPLayer[]
  /** Absent when no layer defines any server. */
  servers?: MCPServerRow[]
  warnings?: string[]
}

/**
 * GET/PUT /api/v1/mcp/{layer}/raw?agent_id= — the literal bytes of that
 * layer's mcp.json. `content` is always present ("" when exists:false).
 */
export interface MCPRawFileResponse {
  layer: string
  path?: string
  exists: boolean
  content: string
  aliased_with?: string[]
  warnings?: string[]
}

/**
 * PUT /api/v1/mcp/{layer}/servers/{name}/toggle[?force=true] response:
 * where the write landed. `layer` echoes the (possibly `auto`) path segment;
 * `effective_layer` is the stored layer the entry now takes effect from.
 */
export interface MCPToggleResponse {
  name: string
  enabled: boolean
  changed: boolean
  removed: boolean
  created: boolean
  layer: string
  path?: string
  effective: MCPEffective
  effective_layer?: string
}

/** POST /api/v1/mcp/validate response. */
export interface MCPValidateResponse {
  valid: boolean
  warnings?: string[]
  error?: string
}
