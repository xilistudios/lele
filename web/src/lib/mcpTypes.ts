export type MCPLayerName = 'global' | 'agent' | 'project'

export type MCPQueryLayer = MCPLayerName | 'auto'

export type MCPEffective = 'enabled' | 'disabled' | 'invalid'

export interface MCPServerSummary {
  kind?: string
  command?: string
  url?: string
  type?: string
  description?: string
  invalid?: string
  args?: number
  disabled?: boolean
  env_keys?: string[]
  header_keys?: string[]
  auth?: MCPServerAuth
}

export interface MCPShadow {
  layer: string
  path?: string
  reason: string
}

export interface MCPServerRow {
  name: string
  layer: string
  path?: string
  effective: MCPEffective
  defines: boolean
  shadowed?: MCPShadow[]
  server: MCPServerSummary
}

export interface MCPLayer {
  layer: string
  path?: string
  exists: boolean
  aliased_with?: string[]
}

export interface MCPInventoryResponse {
  agent_id: string
  layers?: MCPLayer[]
  servers?: MCPServerRow[]
  warnings?: string[]
}

export interface MCPRawFileResponse {
  layer: string
  path?: string
  exists: boolean
  content: string
  aliased_with?: string[]
  warnings?: string[]
}

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

export interface MCPValidateResponse {
  valid: boolean
  warnings?: string[]
  error?: string
}
export interface MCPServerAuth {
  required: boolean
  authenticated: boolean
  method: string
}

export interface MCPAuthStart {
  flow_id: string
  auth_url: string
  redirect_uri: string
  expires_at: string
}

export interface MCPAuthStatus {
  flow_id: string
  server: string
  state: string
  error?: string
  authenticated: boolean
}
