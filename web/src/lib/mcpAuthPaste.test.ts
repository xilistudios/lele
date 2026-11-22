import { describe, expect, test } from 'bun:test'
import { type MCPAuthPaste, parseMCPAuthPaste, sanitizeOAuthCode } from './mcpAuthPaste'

function asCode(result: MCPAuthPaste): Extract<MCPAuthPaste, { kind: 'code' }> {
  if (result.kind !== 'code') throw new Error(`expected a code, got ${result.kind}`)
  return result
}

function asDenied(result: MCPAuthPaste): Extract<MCPAuthPaste, { kind: 'denied' }> {
  if (result.kind !== 'denied') throw new Error(`expected a denial, got ${result.kind}`)
  return result
}

const ok = (code: string): MCPAuthPaste => ({ kind: 'code', code })
const okState = (code: string, state: string): MCPAuthPaste => ({ kind: 'code', code, state })
const invalid: MCPAuthPaste = { kind: 'invalid' }

const GRAMMAR_TABLE: Array<[string, MCPAuthPaste, string]> = [
  ['', { kind: 'empty' }, 'empty'],
  ['   ', { kind: 'empty' }, 'empty'],
  ['abc123', ok('abc123'), 'ok'],
  ['abc-12_3.x~y', ok('abc-12_3.x~y'), 'ok'],
  ['a'.repeat(600), ok('a'.repeat(600)), 'ok (a long, opaque real code)'],
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
  [
    'http://127.0.0.1:37401/cb?code=%zz',
    invalid,
    'unrecognized (malformed escape: Go refuses it, the browser submits "%zz")',
  ],
  [
    'http://127.0.0.1:37401/cb?code=abc&state=%zz',
    invalid,
    'unrecognized (a malformed escape is refused wherever it sits: the surfaces would submit different states)',
  ],
  [
    'http://127.0.0.1:37401/cb?code=%zz#error=access_denied',
    invalid,
    'unrecognized (the query is decoded first, so its bad escape decides)',
  ],
  [
    'http://127.0.0.1:37401/cb?state=%zz#code=abc',
    invalid,
    'unrecognized (the query is scanned ALWAYS, even when the fragment is the substring used)',
  ],
  [
    'http://127.0.0.1:37401/cb?state=%80#code=abc',
    invalid,
    'unrecognized (a high-bit escape in the always-scanned query)',
  ],
  [
    'http://127.0.0.1:37401/cb?state=a;b#code=abc',
    invalid,
    'unrecognized (a `;` in the always-scanned query)',
  ],
  [
    'http://127.0.0.1:37401/cb?state=x#code=%zz',
    invalid,
    'unrecognized (the fragment is scanned when it is the chosen substring)',
  ],
  [
    'http://127.0.0.1:37401/cb?state=x#code=abc',
    ok('abc'),
    'ok (fragment fallback: the query carries no code/error)',
  ],
  [
    'http://127.0.0.1:37401/cb?code=%C3%A9',
    invalid,
    'unrecognized (a high-bit escape: a raw byte in Go, U+FFFD in the browser)',
  ],
  ['http://127.0.0.1:37401/cb?code=a;b', invalid, 'unrecognized (a `;` separator)'],
  [
    'http://127.0.0.1:37401/cb?error=%20&code=abc',
    ok('abc'),
    'ok (a blank `error` is not a refusal)',
  ],
  ['http://127.0.0.1:37401/cb?error=%20', invalid, 'no-code'],
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
    'http://127.0.0.1:37401/cb#?code=abc',
    ok('abc'),
    'ok (the already-agreeing hash carrier: the query slice is already `code=abc`)',
  ],
  [
    'http://127.0.0.1:37401/cb?state=1#?code=abc',
    ok('abc'),
    'ok (hash carrier after an unrelated query)',
  ],
  ['?a=1#?code=abc', ok('abc'), 'ok (bare query then a hash carrier)'],
  [
    'http://127.0.0.1:37401/cb?a=1#?code=abc&state=s',
    okState('abc', 's'),
    'ok (hash carrier carrying a state)',
  ],
  [
    'http://127.0.0.1:37401/cb?a=1#?error=access_denied',
    { kind: 'denied', error: 'access_denied' },
    'denied (hash carrier with a refusal)',
  ],
  [
    '?a=1#??code=abc',
    invalid,
    'no-code (exactly ONE `?` is stripped, never all of them: the first key is the literal `?code`)',
  ],
  ['??code=abc', ok('abc'), 'ok (one leading `?` is stripped by both surfaces)'],
  [
    'http://127.0.0.1:37401/cb#??code=abc',
    ok('abc'),
    'ok (the same strip, reached through the fragment carrier)',
  ],
  [
    '???code=abc',
    invalid,
    'no-code (exactly ONE `?` is stripped: the first key is the literal `?code`)',
  ],
  [
    'http://127.0.0.1:37401/cb?error=access_denied&code=abc',
    { kind: 'denied', error: 'access_denied' },
    'denied (`error` wins over `code`)',
  ],
  ['abc\u0085xyz', invalid, 'unrecognized (U+0085 is not whitespace here)'],
  ['\uFEFFabc123', ok('abc123'), 'ok (a leading BOM is trimmed)'],
  ['abc123\u00A0', ok('abc123'), 'ok (a trailing NBSP is trimmed)'],
]

describe('parseMCPAuthPaste — the frozen shared example table', () => {
  for (const [input, expected, outcome] of GRAMMAR_TABLE) {
    test(`${JSON.stringify(input)} ⇒ ${outcome}`, () => {
      expect(parseMCPAuthPaste(input)).toEqual(expected)
    })
  }

  test('the table is the whole grammar: every row is exercised', () => {
    expect(GRAMMAR_TABLE).toHaveLength(55)
  })
})

describe('parseMCPAuthPaste — accepted forms', () => {
  test('a bare code is the code, with no state', () => {
    expect(asCode(parseMCPAuthPaste('4/0AX4XfWj-_.~+Z')).code).toBe('4/0AX4XfWj-_.~+Z')
    expect(parseMCPAuthPaste('a'.repeat(600))).toEqual({ kind: 'code', code: 'a'.repeat(600) })
  })

  test('a bare token carrying `=` is read as a QUERY, not as a padded code', () => {
    expect(parseMCPAuthPaste('aGVsbG8=')).toEqual(invalid)
    expect(parseMCPAuthPaste('code=abc=')).toEqual({ kind: 'code', code: 'abc=' })
  })

  test('surrounding whitespace is trimmed (a copied value picks it up)', () => {
    expect(parseMCPAuthPaste('\tSPACED-CODE \n')).toEqual({ kind: 'code', code: 'SPACED-CODE' })
  })

  test('a query is passed through its parameters, never submitted verbatim', () => {
    expect(asCode(parseMCPAuthPaste('code=abc')).code).toBe('abc')
    expect(asCode(parseMCPAuthPaste('?code=abc')).code).toBe('abc')
  })

  test('a URL with code only omits the state (never sends "")', () => {
    expect(parseMCPAuthPaste('http://127.0.0.1:41234/callback?code=ONLY')).toEqual({
      kind: 'code',
      code: 'ONLY',
    })
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
    expect(asCode(parseMCPAuthPaste('http://127.0.0.1:1/cb?code=a%2Fb%2Bc&state=s')).code).toBe(
      'a/b+c',
    )
    expect(asCode(parseMCPAuthPaste('http://127.0.0.1:1/cb?code=a+b')).code).toBe('a b')
  })
})

describe('parseMCPAuthPaste — the single input bound', () => {
  test('a 600-character bare code is accepted (there is no per-code rule)', () => {
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
    const prefix = 'http://127.0.0.1:37401/cb?code='
    expect(parseMCPAuthPaste(prefix + 'a'.repeat(9000))).toEqual(invalid)
    const room = 8192 - prefix.length
    expect(asCode(parseMCPAuthPaste(prefix + 'a'.repeat(room))).code).toBe('a'.repeat(room))
  })

  test('the bound is measured in BYTES, not `.length` code units', () => {
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
    expect(JSON.stringify(result)).not.toContain('nope')
  })

  test('garbage is "invalid", never a code', () => {
    expect(parseMCPAuthPaste('http://127.0.0.1:41234/callback?state=st-1')).toEqual(invalid)
    expect(parseMCPAuthPaste('https://gw.example/cb?code=')).toEqual(invalid)
    expect(parseMCPAuthPaste('http://')).toEqual(invalid)
    expect(parseMCPAuthPaste('not a code !!')).toEqual(invalid)
    expect(parseMCPAuthPaste('!!!')).toEqual(invalid)
    expect(parseMCPAuthPaste('foo=bar')).toEqual(invalid)
    expect(parseMCPAuthPaste('state=xyz')).toEqual(invalid)
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

describe('parseMCPAuthPaste — the parameter-substring scan', () => {
  test('a short escape ("%8") is a malformed escape, not a byte', () => {
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb?code=%8')).toEqual(invalid)
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb?code=abc%8')).toEqual(invalid)
    expect(parseMCPAuthPaste('?state=%8')).toEqual(invalid)
  })

  test('a trailing `%` is a malformed escape', () => {
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb?code=abc%')).toEqual(invalid)
    expect(parseMCPAuthPaste('code=abc%')).toEqual(invalid)
  })

  test('`%FF` and `%ff` are refused alike (the high-bit rule is case-insensitive)', () => {
    expect(parseMCPAuthPaste('?code=%FF')).toEqual(invalid)
    expect(parseMCPAuthPaste('?code=%ff')).toEqual(invalid)
    expect(parseMCPAuthPaste('?state=%Ff')).toEqual(invalid)
    expect(parseMCPAuthPaste('?code=%F0%9F%A6%9E')).toEqual(invalid)
  })

  test('the high-bit boundary is the BYTE: `%80` is refused, `%7F` is not', () => {
    expect(parseMCPAuthPaste('?code=%80')).toEqual(invalid)
    expect(asCode(parseMCPAuthPaste('?code=%7F')).code).toBe('\u007f')
    expect(asCode(parseMCPAuthPaste('?code=%7e')).code).toBe('~')
  })

  test('ASCII escapes still decode, so `+` ⇒ space keeps working', () => {
    expect(asCode(parseMCPAuthPaste('?code=a%2Bb%20c')).code).toBe('a+b c')
    expect(parseMCPAuthPaste('?code=a+b')).toEqual({ kind: 'code', code: 'a b' })
  })

  test('`;` is the only separator refused: other punctuation is still a code', () => {
    expect(parseMCPAuthPaste('?code=a;b')).toEqual(invalid)
    expect(parseMCPAuthPaste('?code=a;b&state=s')).toEqual(invalid)
    expect(parseMCPAuthPaste('?state=s;code=abc')).toEqual(invalid)
    expect(parseMCPAuthPaste('?code=a:b')).toEqual({ kind: 'code', code: 'a:b' })
    expect(parseMCPAuthPaste('?code=a,b')).toEqual({ kind: 'code', code: 'a,b' })
    expect(parseMCPAuthPaste('?code=a|b')).toEqual({ kind: 'code', code: 'a|b' })
  })

  test('the scan runs on the RAW substring, before decoding', () => {
    expect(parseMCPAuthPaste('?code=a%3Bb')).toEqual({ kind: 'code', code: 'a;b' })
  })

  test('the QUERY is scanned ALWAYS, the fragment when it is chosen', () => {
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb?state=%zz#code=abc')).toEqual(invalid)
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb?state=x#code=%zz')).toEqual(invalid)
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb#code=%zz')).toEqual(invalid)
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb#code=a;b')).toEqual(invalid)
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb?state=x#?code=%zz')).toEqual(invalid)
    expect(parseMCPAuthPaste('http://127.0.0.1:1/cb?state=x#code=abc')).toEqual(ok('abc'))
  })

  test('a refusal is never an echo: nothing of the paste survives the scan', () => {
    const canary = 'CANARY-bad-escape'
    const result = parseMCPAuthPaste(`http://127.0.0.1:1/cb?code=%zz&x=${canary}`)
    expect(result).toEqual(invalid)
    expect(JSON.stringify(result)).not.toContain(canary)
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
