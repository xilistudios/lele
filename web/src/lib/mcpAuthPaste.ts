export type MCPAuthPaste =
  | { kind: 'code'; code: string; state?: string }
  | { kind: 'empty' }
  | { kind: 'denied'; error?: string }
  | { kind: 'invalid' }

const CODE_SHAPE_RE = /^[A-Za-z0-9\-._~+/=]+$/

const MAX_INPUT_BYTES = 8192

const RESPONSE_KEYS = ['code', 'error'] as const

const PARAMETER_KEYS = ['code', 'state', 'error'] as const

const ECHOABLE_CODE_RE = /^[A-Za-z0-9_.-]{1,64}$/

export function sanitizeOAuthCode(value: string): string {
  return ECHOABLE_CODE_RE.test(value) ? value : ''
}

const UNSAFE_FORM_ENCODING_RE = /%(?![0-9A-Fa-f]{2})|%[89A-Fa-f][0-9A-Fa-f]|;/

function hasUnsafeFormEncoding(parameterSubstring: string): boolean {
  return UNSAFE_FORM_ENCODING_RE.test(parameterSubstring)
}

function isCodeShape(token: string): boolean {
  return CODE_SHAPE_RE.test(token)
}

function responseParams(value: string): URLSearchParams | null {
  const question = value.indexOf('?')
  let query = question === -1 ? value : value.slice(question + 1)
  const hashInQuery = query.indexOf('#')
  if (hashInQuery !== -1) query = query.slice(0, hashInQuery)

  if (hasUnsafeFormEncoding(query)) return null

  const queryParams = new URLSearchParams(query)
  const hash = value.indexOf('#')
  if (hash === -1 || RESPONSE_KEYS.some((key) => queryParams.has(key))) {
    return queryParams
  }

  const fragment = value.slice(hash + 1)
  return hasUnsafeFormEncoding(fragment) ? null : new URLSearchParams(fragment)
}

function resolveParameters(params: URLSearchParams): MCPAuthPaste {
  const error = (params.get('error') ?? '').trim()
  if (error !== '') {
    const safe = sanitizeOAuthCode(error)
    return safe === '' ? { kind: 'denied' } : { kind: 'denied', error: safe }
  }

  const code = (params.get('code') ?? '').trim()
  if (code === '') return { kind: 'invalid' }
  const state = (params.get('state') ?? '').trim()
  return state === '' ? { kind: 'code', code } : { kind: 'code', code, state }
}

export function parseMCPAuthPaste(raw: string): MCPAuthPaste {
  if (new TextEncoder().encode(raw).length > MAX_INPUT_BYTES) return { kind: 'invalid' }

  const value = raw.trim()
  if (value === '') return { kind: 'empty' }

  if (/\s/.test(value)) {
    const fields = value.split(/\s+/)
    if (fields.length !== 2) return { kind: 'invalid' }
    const [code, state] = fields
    if ([code, state].some((field) => field.includes('?') || field.includes('='))) {
      return { kind: 'invalid' }
    }
    if (!isCodeShape(code) || !isCodeShape(state)) return { kind: 'invalid' }
    return { kind: 'code', code, state }
  }

  if (value.includes('?')) {
    const params = responseParams(value)
    return params === null ? { kind: 'invalid' } : resolveParameters(params)
  }

  if (value.includes('=')) {
    const params = responseParams(value)
    if (params === null) return { kind: 'invalid' }
    if (!PARAMETER_KEYS.some((key) => params.has(key))) return { kind: 'invalid' }
    return resolveParameters(params)
  }

  return isCodeShape(value) ? { kind: 'code', code: value } : { kind: 'invalid' }
}
