import { describe, expect, test } from 'bun:test'
import { type MCPAuthPaste, parseMCPAuthPaste, sanitizeOAuthCode } from './mcpAuthPaste'

/**
 * Unit contract of the manual-completion parser (the modal's paste box).
 *
 * The grammar is FROZEN in `.lele/tasks/mcp-oauth-paste-grammar.md` and shared
 * by the TUI, the CLI and this surface; the first suite below is that
 * document's own example table, transcribed 1:1 (including the two rows the
 * grammar folds into one — `unrecognized` and `no-code` are both rendered as
 * `invalid` here, which the document explicitly allows; the input SET is what
 * may not differ, and it is pinned row by row).
 *
 * The failure modes this suite guards are the ones that would silently corrupt a
 * flow: a `error=access_denied` redirect parsed as a code (the exchange then
 * fails with an opaque server error instead of "you denied it"), a query
 * submitted VERBATIM as the code (`code=abc` reaching the token endpoint as
 * the literal `"code=abc"`), garbage reaching the endpoint at all (which the
 * server answers with `invalid_request` while the user believes they submitted
 * something valid), and a server-chosen byte string echoed into the UI.
 */

/** Narrow a result to the `code` arm, failing loudly (not on a cast) otherwise. */
function asCode(result: MCPAuthPaste): Extract<MCPAuthPaste, { kind: 'code' }> {
  if (result.kind !== 'code') throw new Error(`expected a code, got ${result.kind}`)
  return result
}

/** Narrow a result to the `denied` arm. */
function asDenied(result: MCPAuthPaste): Extract<MCPAuthPaste, { kind: 'denied' }> {
  if (result.kind !== 'denied') throw new Error(`expected a denial, got ${result.kind}`)
  return result
}

/** `ok(code, "")` of the grammar, with the empty state OMITTED (see below). */
const ok = (code: string): MCPAuthPaste => ({ kind: 'code', code })
const okState = (code: string, state: string): MCPAuthPaste => ({ kind: 'code', code, state })
/** The grammar's `unrecognized` and `no-code`, both rendered as `invalid`. */
const invalid: MCPAuthPaste = { kind: 'invalid' }

/**
 * The shared example table of the frozen grammar. Every other surface carries
 * the same rows (Go: `pkg/tui` + `cmd/lele`), so a divergence is a failing row
 * here rather than a surprised user.
 */
const GRAMMAR_TABLE: Array<[string, MCPAuthPaste, string]> = [
  ['', { kind: 'empty' }, 'empty'],
  ['   ', { kind: 'empty' }, 'empty'],
  ['abc123', ok('abc123'), 'ok'],
  ['abc-12_3.x~y', ok('abc-12_3.x~y'), 'ok'],
  // No per-code length rule: long, opaque real codes are accepted...
  ['a'.repeat(600), ok('a'.repeat(600)), 'ok (a long, opaque real code)'],
  // ...while the single input bound refuses an oversized paste before any rule.
  ['a'.repeat(9000), invalid, 'unrecognized (the input bound)'],
  ['  abc123  ', ok('abc123'), 'ok'],
  ['abc123\n', ok('abc123'), 'ok'],
  ['!!!', invalid, 'unrecognized'],
  ['abc!def', invalid, 'unrecognized'],
  ['a b c', invalid, 'unrecognized'],
  ['abc xyz', okState('abc', 'xyz'), 'ok'],
  ['abc   xyz', okState('abc', 'xyz'), 'ok'],
  ['abc! xyz', invalid, 'unrecognized'],
  ['code=abc', ok('abc'), 'ok'],
  ['code=abc&state=xyz', okState('abc', 'xyz'), 'ok'],
  ['?code=abc&state=xyz', okState('abc', 'xyz'), 'ok'],
  ['http://127.0.0.1:37401/cb?code=abc&state=xyz', okState('abc', 'xyz'), 'ok (full redirect URL)'],
  [
    'http://127.0.0.1:37401/cb?state=xyz&code=abc',
    okState('abc', 'xyz'),
    'ok (parameter order is irrelevant)',
  ],
  ['http://127.0.0.1:37401/cb?code=a%2Bb&state=s', okState('a+b', 's'), 'ok (percent-decoded)'],
  [
    'http://127.0.0.1:37401/cb?code=abc#frag',
    ok('abc'),
    'ok (a fragment is not part of the query)',
  ],
  ['http://127.0.0.1:37401/cb?code=', invalid, 'no-code'],
  ['http://127.0.0.1:37401/cb?state=xyz', invalid, 'no-code'],
  [
    'http://127.0.0.1:37401/cb?error=access_denied',
    { kind: 'denied', error: 'access_denied' },
    'denied',
  ],
  [
    'http://127.0.0.1:37401/cb?error=access_denied&error_description=ATTACKER_PROSE',
    { kind: 'denied', error: 'access_denied' },
    'denied (error_description is never echoed)',
  ],
  [
    'http://127.0.0.1:37401/cb?error=%1B%5B31mhax',
    { kind: 'denied' },
    'denied (unsafe code dropped)',
  ],
  ['foo=bar', invalid, 'unrecognized (`=` with none of code/state/error)'],
  ['code=abc def', invalid, 'unrecognized (two tokens, one carrying `=`)'],
  [
    'http://127.0.0.1:37401/cb#code=abc&state=s',
    okState('abc', 's'),
    'ok (fragment fallback: response_mode=fragment)',
  ],
  [
    'http://127.0.0.1:37401/cb?state=xyz#code=abc',
    ok('abc'),
    'ok (query has no code/error, so the fragment answers)',
  ],
  [
    'http://127.0.0.1:37401/cb?error=access_denied&code=abc',
    { kind: 'denied', error: 'access_denied' },
    'denied (`error` wins over `code`)',
  ],
]

describe('parseMCPAuthPaste — the frozen shared example table', () => {
  for (const [input, expected, outcome] of GRAMMAR_TABLE) {
    test(`${JSON.stringify(input)} ⇒ ${outcome}`, () => {
      expect(parseMCPAuthPaste(input)).toEqual(expected)
    })
  }

  test('the table is the whole grammar: every row is exercised', () => {
    // Guards against a future edit that drops rows instead of fixing them.
    expect(GRAMMAR_TABLE).toHaveLength(31)
  })
})

describe('parseMCPAuthPaste — accepted forms', () => {
  test('a bare code is the code, with no state', () => {
    // the base64url / Google-style alphabet gateways really emit
    expect(asCode(parseMCPAuthPaste('4/0AX4XfWj-_.~+Z')).code).toBe('4/0AX4XfWj-_.~+Z')
    // The shape check has NO length rule: a code is opaque to the client
    // (RFC 6749 §A.11) and encrypted codes run past 1000 bytes in the field, so
    // a 600-character code is a perfectly valid paste. (A 512-char cap used to
    // refuse exactly this; the single input bound is what keeps a paste finite.)
    expect(parseMCPAuthPaste('a'.repeat(600))).toEqual({ kind: 'code', code: 'a'.repeat(600) })
  })

  test('a bare token carrying `=` is read as a QUERY, not as a padded code', () => {
    // The documented trade-off of the frozen grammar: `=` with no `?` selects
    // the parameter form, so an (unpadded-in-practice) base64 code must be
    // pasted as the full redirect URL. The alternative — guessing — would
    // submit obvious garbage like `foo=bar` as if it were a code.
    expect(parseMCPAuthPaste('aGVsbG8=')).toEqual(invalid)
    expect(parseMCPAuthPaste('code=abc=')).toEqual({ kind: 'code', code: 'abc=' })
  })

  test('surrounding whitespace is trimmed (a copied value picks it up)', () => {
    expect(parseMCPAuthPaste('\tSPACED-CODE \n')).toEqual({ kind: 'code', code: 'SPACED-CODE' })
  })

  test('a query is passed through its parameters, never submitted verbatim', () => {
    // The regression this pins: `code=abc` used to be submitted as the literal
    // string "code=abc" as the authorization code.
    expect(asCode(parseMCPAuthPaste('code=abc')).code).toBe('abc')
    expect(asCode(parseMCPAuthPaste('?code=abc')).code).toBe('abc')
  })

  test('a URL with code only omits the state (never sends "")', () => {
    expect(parseMCPAuthPaste('http://127.0.0.1:41234/callback?code=ONLY')).toEqual({
      kind: 'code',
      code: 'ONLY',
    })
    // an empty state parameter is as good as absent
    expect(parseMCPAuthPaste('https://gw.example/cb?code=ONLY&state=')).toEqual({
      kind: 'code',
      code: 'ONLY',
    })
  })

  test('extra parameters are ignored — only code/state/error are read', () => {
    expect(
      parseMCPAuthPaste('https://gw.example/cb?iss=https%3A%2F%2Fas&code=C1&state=s1&scope=a+b'),
    ).toEqual({ kind: 'code', code: 'C1', state: 's1' })
  })

  test('form-encoding is decoded exactly like the Go server does it', () => {
    // URLSearchParams turns "+" into a space, and so does url.ParseQuery on the
    // same bytes: client and server must agree on the code they exchange.
    expect(asCode(parseMCPAuthPaste('http://127.0.0.1:1/cb?code=a%2Fb%2Bc&state=s')).code).toBe(
      'a/b+c',
    )
    expect(asCode(parseMCPAuthPaste('http://127.0.0.1:1/cb?code=a+b')).code).toBe('a b')
  })
})

describe('parseMCPAuthPaste — the single input bound', () => {
  // The grammar's ONLY size rule: a paste longer than 8192 BYTES is
  // `unrecognized` whatever its shape, measured on the RAW input before any
  // other rule runs. It mirrors Go's `len(input) > authPasteMaxInputLen`
  // exactly, so the same paste gets the same answer on both surfaces.

  test('a 600-character bare code is accepted (there is no per-code rule)', () => {
    // A Microsoft JWE-wrapped code runs past 1000 bytes; any cap here would
    // refuse a paste that works.
    expect(parseMCPAuthPaste('a'.repeat(600))).toEqual({ kind: 'code', code: 'a'.repeat(600) })
  })

  test('a 9000-character bare code is refused by the input bound', () => {
    expect(parseMCPAuthPaste('a'.repeat(9000))).toEqual(invalid)
  })

  test('the bound is exact: an 8192-byte input parses, an 8193-byte one does not', () => {
    const atBound = 'a'.repeat(8192)
    expect(parseMCPAuthPaste(atBound)).toEqual({ kind: 'code', code: atBound })
    expect(parseMCPAuthPaste('a'.repeat(8193))).toEqual(invalid)
  })

  test('the bound also reaches a `code=` PARAMETER the shape check never sees', () => {
    // A parameter code is otherwise kept verbatim and opaque (RFC 6749 §A.11),
    // so only the input bound can refuse it.
    const prefix = 'http://127.0.0.1:37401/cb?code='
    expect(parseMCPAuthPaste(prefix + 'a'.repeat(9000))).toEqual(invalid)
    // a parameter code that fits the bound is still submitted verbatim
    const room = 8192 - prefix.length
    expect(asCode(parseMCPAuthPaste(prefix + 'a'.repeat(room))).code).toBe('a'.repeat(room))
  })

  test('the bound is measured in BYTES, not `.length` code units', () => {
    // A parameter code is opaque (no shape check), so this paste is refused ONLY
    // if the bound counts UTF-8 bytes: 31 ASCII bytes of prefix + 4081 two-byte
    // runes = 8193 bytes, yet only 4112 code units. `.length` would accept it and
    // submit the runes as a code; Go's `len(input)` refuses it, and so must this.
    const prefix = 'http://127.0.0.1:37401/cb?code='
    const input = prefix + 'é'.repeat(4081)
    expect(input.length).toBeLessThanOrEqual(8192)
    expect(new TextEncoder().encode(input).length).toBe(8193)
    expect(parseMCPAuthPaste(input)).toEqual(invalid)
  })

  test('a hostile oversized paste is refused with nothing echoed', () => {
    const canary = 'CANARY-oversized-paste-must-never-be-echoed'
    const hostile = `${canary}\u001b[31m${'a'.repeat(9000)}`
    const result = parseMCPAuthPaste(hostile)
    expect(result).toEqual(invalid)
    const json = JSON.stringify(result)
    expect(json).not.toContain(canary)
    expect(json).not.toContain('\u001b')
  })
})

describe('parseMCPAuthPaste — refusals and garbage never submit', () => {
  test('error=access_denied is a failure, not a bogus code', () => {
    const result = parseMCPAuthPaste(
      'http://127.0.0.1:41234/callback?error=access_denied&state=st-1&error_description=nope',
    )
    expect(result).toEqual({ kind: 'denied', error: 'access_denied' })
    expect(result.kind).not.toBe('code')
    // The AS's own prose never survives the parse, not even as a field.
    expect(JSON.stringify(result)).not.toContain('nope')
  })

  test('garbage is "invalid", never a code', () => {
    // shaped like a URL but carrying neither code nor error
    expect(parseMCPAuthPaste('http://127.0.0.1:41234/callback?state=st-1')).toEqual(invalid)
    expect(parseMCPAuthPaste('https://gw.example/cb?code=')).toEqual(invalid)
    // a scheme with nothing behind it is not a URL at all (and not a code)
    expect(parseMCPAuthPaste('http://')).toEqual(invalid)
    expect(parseMCPAuthPaste('not a code !!')).toEqual(invalid)
    expect(parseMCPAuthPaste('!!!')).toEqual(invalid)
    // `=`-carrying values must name a response parameter to be readable
    expect(parseMCPAuthPaste('foo=bar')).toEqual(invalid)
    expect(parseMCPAuthPaste('state=xyz')).toEqual(invalid)
    // a bare code is not split across lines: whitespace means the two-token form
    expect(parseMCPAuthPaste('abc def ghi')).toEqual(invalid)
  })

  test('the two-token form refuses anything but two well-shaped tokens', () => {
    expect(parseMCPAuthPaste('abc xyz')).toEqual({ kind: 'code', code: 'abc', state: 'xyz' })
    expect(parseMCPAuthPaste('abc! xyz')).toEqual(invalid)
    expect(parseMCPAuthPaste('abc xyz!')).toEqual(invalid)
    expect(parseMCPAuthPaste('code=abc def')).toEqual(invalid)
    expect(parseMCPAuthPaste('?code=abc state')).toEqual(invalid)
    expect(parseMCPAuthPaste('a b c')).toEqual(invalid)
  })
})

describe('sanitizeOAuthCode — the only echoable server value', () => {
  test('a short ASCII OAuth word survives', () => {
    expect(sanitizeOAuthCode('access_denied')).toBe('access_denied')
    expect(sanitizeOAuthCode('invalid_grant')).toBe('invalid_grant')
    expect(sanitizeOAuthCode('a.b-c_1')).toBe('a.b-c_1')
    expect(sanitizeOAuthCode('a'.repeat(64))).toBe('a'.repeat(64))
  })

  test('anything else becomes "" (nothing safe to echo)', () => {
    expect(sanitizeOAuthCode('')).toBe('')
    expect(sanitizeOAuthCode('a'.repeat(65))).toBe('')
    expect(sanitizeOAuthCode('\u001b[31mhax')).toBe('')
    expect(sanitizeOAuthCode('two words')).toBe('')
    expect(sanitizeOAuthCode('semi;colon')).toBe('')
    expect(sanitizeOAuthCode('<script>')).toBe('')
    expect(sanitizeOAuthCode('a\nb')).toBe('')
    expect(sanitizeOAuthCode('café')).toBe('')
  })

  test('no ESC or control byte can survive the parser', () => {
    // Measured before this rule: the raw parameter reached the UI paragraph.
    const denied = asDenied(
      parseMCPAuthPaste('http://127.0.0.1:41234/callback?error=%1B%5B31mhax&state=s'),
    )
    expect(denied.error).toBeUndefined()
    expect(JSON.stringify(denied)).not.toContain('\u001b')
  })

  test('a URL-encoded control byte is dropped from a query, not just a URL', () => {
    const denied = asDenied(parseMCPAuthPaste('?error=%00%07%1b%5b%33%31mhax'))
    expect(denied.error).toBeUndefined()
  })
})
