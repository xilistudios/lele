/**
 * mcpAuthPaste — the PURE half of the MCP OAuth manual completion path, and the
 * WebUI HALF OF A THREE-SURFACE GRAMMAR.
 *
 * The grammar itself is FROZEN in `.lele/tasks/mcp-oauth-paste-grammar.md`: the
 * TUI (`ModalMCPAuth`), the CLI (`lele mcp auth --manual`) and this module all
 * accept the same pastes and refuse the same pastes. A client review proved the
 * three surfaces disagreed — the same paste was submitted, refused, or
 * submitted AS A DIFFERENT CODE depending on where it was typed — so the
 * decision tree, the accepted forms, the shape check, the sanitizer and the
 * result vocabulary below are a transcription of that document, not a local
 * policy. Change them only there first.
 *
 * A gateway running on another machine (or headless) cannot receive the
 * browser's loopback callback, so the user hands the redirect back by hand:
 * they paste what the browser was left with into the modal's box. The frozen
 * grammar accepts ONE of
 *
 *   1. the full redirect URL (`http://127.0.0.1:37401/cb?code=…&state=…`),
 *   2. the bare query string, with or without its leading `?`
 *      (`?code=…&state=…`, `code=…&state=…`),
 *   3. a bare authorization `code` (`SplxlOBeZQQYbYS6WxSbIA`),
 *   4. two whitespace-separated tokens, `<code> <state>` — a terminal
 *      convenience for ASs that display the two on separate lines.
 *
 * This module turns the accepted forms into the `{code, state?}` body that
 * `POST /api/v1/mcp/auth/{flow_id}/complete` expects (frozen REST contract,
 * `.lele/tasks/mcp-oauth-rest-contract.md`). It is deliberately pure and lives
 * outside the modal so the parsing rules are unit-testable without a DOM.
 *
 * Three rules it exists for:
 *
 *  - `error=access_denied` (the user pressed "deny" at the AS) is a FAILURE,
 *    never a code. Such a URL carries no `code`, and inventing one would trade
 *    a clear "you denied this" for an opaque exchange error from the server.
 *  - GARBAGE MUST NOT SUBMIT: an empty box, a redirect/query carrying neither
 *    `code` nor `error`, a value whose parameters name none of
 *    `code`/`state`/`error` (`foo=bar`), a paste of more than two tokens, or a
 *    token outside the RFC 6749 alphabet resolves to a non-`code` result, and
 *    the caller keeps its submit control disabled.
 *  - AN ATTACKER-CONTROLLED VALUE IS NEVER ECHOED: only the redirect's `error`
 *    code passes `sanitizeOAuthCode` (a bounded `[A-Za-z0-9_.-]` word), and
 *    `error_description` / every other parameter is ignored on purpose — it is
 *    attacker-controlled prose.
 *
 * RESULT VOCABULARY: the grammar names five outcomes (`ok`, `empty`,
 * `unrecognized`, `no-code`, `denied`). The union below keeps four kinds
 * because the document explicitly allows a surface to render `unrecognized`
 * and `no-code` with one sentence — both mean "there is no code in what you
 * pasted" — so both map to `invalid`. What is NOT allowed, and what the tests
 * pin against the shared table, is accepting or refusing a different SET of
 * inputs than the frozen table.
 *
 * URL/query parsing is delegated to `URLSearchParams` (never hand-rolled): the
 * browser's own decoder decides what a parameter is — including the `+` → space
 * decoding of `application/x-www-form-urlencoded` values, which is exactly what
 * Go's `url.ParseQuery` does with the same bytes, so client and server agree on
 * the code. The query is sliced out by the grammar's own rule (everything after
 * the first `?`, truncated at the first `#`) rather than by `new URL`, because
 * the paste is just as often a bare query with no scheme for `URL` to accept.
 *
 * Nothing here reads or returns a secret: an authorization `code` is a
 * single-use, already-spent-by-the-time-it-matters token the USER pasted, and
 * it is forwarded, never rendered.
 */

/** The parsed intent of ONE pasted value. */
export type MCPAuthPaste =
  | { kind: 'code'; code: string; state?: string }
  /** Nothing usable in the box (empty/whitespace only) — the submit stays disabled. */
  | { kind: 'empty' }
  /**
   * The AS reported a refusal (`error=access_denied`, `error=…`): NOT a code.
   * `error` is the SANITIZED code (`sanitizeOAuthCode`); it is ABSENT when the
   * server's value did not qualify, in which case the caller renders its own
   * fixed sentence — never the raw parameter (see the sanitizer below).
   */
  | { kind: 'denied'; error?: string }
  /**
   * The frozen `unrecognized` OR `no-code` outcome — shaped like neither a code
   * nor a redirect/query with a code. Never submitted.
   */
  | { kind: 'invalid' }

/**
 * Shape of a bare `code` (or of each of the two whitespace-separated tokens):
 * non-empty, and no character outside the RFC 6749 §A.11 `code` alphabet plus
 * the base64 padding character — `A-Za-z0-9 - . _ ~ + / =`.
 *
 * The check is the CHARSET only: there is deliberately NO length rule here.
 * RFC 6749 §A.11 makes a code opaque to the client, and encrypted codes are long
 * in the field (a Microsoft JWE-wrapped code runs past 1000 bytes), so any cap
 * would reject pastes that work. The size guarantee lives in the grammar's single
 * INPUT BOUND (`MAX_INPUT_BYTES`), which runs before this check and applies to
 * every form; a code that arrived as a `code=` PARAMETER never passes through
 * here at all and is kept verbatim and opaque.
 *
 * What the charset still rejects is what can never be a code: whitespace,
 * control characters and punctuation like `!`, `?`, `%`, `#`, `:`. Note the
 * trade-off the frozen grammar states: a bare token containing `=` is read as a
 * QUERY STRING, not as a padded code, so a (legal but unusual) padded code must
 * be pasted as the full redirect URL.
 */
const CODE_SHAPE_RE = /^[A-Za-z0-9\-._~+/=]+$/

/**
 * The grammar's ONLY size rule — the INPUT BOUND. A paste longer than this many
 * BYTES is `invalid` before any other rule runs (see `parseMCPAuthPaste`). It
 * exists so no surface feeds an arbitrary amount of pasted bytes into a request;
 * 8 KiB is far above any redirect URL a browser produces, so it never refuses a
 * real paste.
 *
 * It deliberately does NOT bound an authorization code: a code is opaque
 * (RFC 6749 §A.11) and the long encrypted codes above must keep working. The
 * bound is measured in bytes — `TextEncoder`, Go's `len` — so the two surfaces
 * agree on the same input, in a browser and in Bun alike.
 */
const MAX_INPUT_BYTES = 8192

/** OAuth response parameters, in the grammar's precedence order. */
const RESPONSE_KEYS = ['code', 'error'] as const

/** Every parameter the `=`-carrying form must name to not be `unrecognized`. */
const PARAMETER_KEYS = ['code', 'state', 'error'] as const

/**
 * The sanitizer — the ONLY thing from a server that may ever be echoed, and
 * the mirror of the server-side rule (`pkg/mcp` `sanitizeOAuthToken`, copied as
 * `mcpAuthOAuthToken` in the CLI and `mcpAuthEchoableToken` in the TUI, both of
 * which document the duplication). The frozen grammar exports the same rule to
 * every surface so they stop re-implementing it.
 *
 * Returns the value iff it is non-empty, at most 64 characters long and made
 * ENTIRELY of `A-Za-z0-9_.-`; otherwise `''`, meaning "nothing safe to echo".
 * This bounds the echo: a hostile authorization server can choose a short ASCII
 * word that is displayed, and nothing else — never a body, never a token, never
 * a control character or an escape sequence (measured before this rule:
 * `?error=%1B%5B31mhax` put a raw `\u001b[31mhax` into the UI paragraph).
 */
const ECHOABLE_CODE_RE = /^[A-Za-z0-9_.-]{1,64}$/

/** The sanitized form of a server-provided OAuth code (see above). */
export function sanitizeOAuthCode(value: string): string {
  return ECHOABLE_CODE_RE.test(value) ? value : ''
}

/**
 * Shape check of the frozen grammar: is this token a possible bare code?
 * (`CODE_SHAPE_RE` — non-empty and RFC 6749 alphabet only, no length rule.)
 */
function isCodeShape(token: string): boolean {
  return CODE_SHAPE_RE.test(token)
}

/**
 * The parameters of ONE whitespace-free paste, per the grammar's parameter
 * form: first the QUERY — everything after the first `?`, truncated at the
 * first `#`, or the whole value when it carries no `?` — then, only when that
 * query carries neither `code` nor `error`, the FRAGMENT (everything after the
 * first `#`).
 *
 * The fragment fallback is deliberate and was tested here before the grammar
 * existed: an authorization server answering with `response_mode=fragment`
 * leaves `…/callback#code=…&state=…` in the address bar, the browser sends a
 * fragment nowhere, so the paste is the only place that response exists — it
 * can only ever rescue a user.
 */
function responseParams(value: string): URLSearchParams {
  const question = value.indexOf('?')
  let query = question === -1 ? value : value.slice(question + 1)
  const hashInQuery = query.indexOf('#')
  if (hashInQuery !== -1) query = query.slice(0, hashInQuery)

  const params = new URLSearchParams(query)
  if (RESPONSE_KEYS.some((key) => params.has(key))) return params
  const hash = value.indexOf('#')
  return hash === -1 ? params : new URLSearchParams(value.slice(hash + 1))
}

/**
 * The grammar's parameter-form resolution, in its exact order: a non-empty
 * `error` wins over a `code` even when both are present; then a non-empty
 * `code`; then the frozen `no-code` outcome.
 */
function resolveParameters(params: URLSearchParams): MCPAuthPaste {
  // `error_description` and every other parameter are deliberately ignored:
  // they are attacker-controlled prose, and only the error CODE is echoable.
  const error = (params.get('error') ?? '').trim()
  if (error !== '') {
    const safe = sanitizeOAuthCode(error)
    // No safe code ⇒ the caller shows its own fixed sentence; the variant
    // simply carries no `error`.
    return safe === '' ? { kind: 'denied' } : { kind: 'denied', error: safe }
  }

  // Verbatim, trimmed — the grammar applies NO shape check to a parameter code
  // (it came out of a URL the user's browser produced).
  const code = (params.get('code') ?? '').trim()
  if (code === '') return { kind: 'invalid' } // frozen `no-code`
  const state = (params.get('state') ?? '').trim()
  // `state` is optional on the wire; an empty one is OMITTED, never sent as ''.
  return state === '' ? { kind: 'code', code } : { kind: 'code', code, state }
}

/**
 * Parse one pasted value — the frozen decision tree, in its exact order.
 * Whitespace around the whole input is ignored (a copied URL routinely picks up
 * a trailing newline); a value WITH whitespace inside is only ever the
 * two-token form, never a code split across lines.
 */
export function parseMCPAuthPaste(raw: string): MCPAuthPaste {
  // 0. The INPUT BOUND — the grammar's ONLY size rule — runs FIRST, before any
  //    other rule sees the bytes: a paste longer than 8 KiB is `invalid`
  //    whatever its shape. It exists so no surface feeds an arbitrary amount of
  //    pasted bytes into a request; 8 KiB is far above any redirect URL a
  //    browser produces, so no real paste is refused here. Measure BYTES, not
  //    `.length` code units, so this agrees exactly with Go's `len(input)`
  //    (both run in the browser and in Bun).
  if (new TextEncoder().encode(raw).length > MAX_INPUT_BYTES) return { kind: 'invalid' }

  const value = raw.trim()
  if (value === '') return { kind: 'empty' }

  // 3. whitespace ⇒ two whitespace-separated tokens (`<code> <state>`), split
  //    on runs of whitespace. Anything else is refused: `a b c`,
  //    `code=abc def` and `abc! xyz` are rejections, so a refusal URL is never
  //    mistaken for a two-token paste.
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

  // 2a. `?` ⇒ parameter form (a full URL, or the query pasted on its own).
  if (value.includes('?')) return resolveParameters(responseParams(value))

  // 2b. `=` ⇒ also the parameter form: this is what makes `foo=bar` a
  //     rejection instead of a submitted code. A value whose parameters name
  //     none of code/state/error is the frozen `unrecognized` outcome.
  if (value.includes('=')) {
    const params = responseParams(value)
    if (!PARAMETER_KEYS.some((key) => params.has(key))) return { kind: 'invalid' }
    return resolveParameters(params)
  }

  // 2c. a bare code: it must pass the shape check.
  return isCodeShape(value) ? { kind: 'code', code: value } : { kind: 'invalid' }
}
