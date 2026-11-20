// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

/**
 * Pure logic for the Claude Agent SDK bridge.
 *
 * This module NEVER imports `@anthropic-ai/claude-agent-sdk` or
 * `@modelcontextprotocol/sdk` at load time: every SDK import happens lazily
 * inside the resolver it belongs to. That keeps the module importable for unit
 * tests (node --test bridge_test.mjs) on a machine where the SDK is not
 * installed, which is also how lele ships it (embedded in the Go binary).
 *
 * Nothing here touches stdio or process state; bridge.mjs owns the wire.
 */

import { existsSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

/** npm name of the SDK the bridge drives. */
export const SDK_PACKAGE = '@anthropic-ai/claude-agent-sdk';

/** MCP server name lele's tools are exposed under (SDK prefixes tools with it). */
export const MCP_SERVER_NAME = 'lele';

/** MCP server version reported to Claude Code (arbitrary, informational). */
export const MCP_SERVER_VERSION = '1.0.0';

/** Prefix the SDK puts in front of every tool served by the `lele` MCP server. */
export const MCP_TOOL_PREFIX = `mcp__${MCP_SERVER_NAME}__`;

/**
 * Text the MCP `CallTool` handler answers with. lele never executes tools
 * inside Claude Code; with `canUseTool` denying every call this handler is only
 * a safety net.
 */
export const CAPTURED_TOOL_TEXT =
  'Tool call captured by lele; execution happens outside Claude Code.';

/** Message `canUseTool` denies with (defense in depth, see module docs). */
export const DENY_MESSAGE = 'Captured by lele.';

/** Hint appended to every auth-classified failure. */
export const AUTH_HINT =
  "Run 'claude' and log in with your Claude subscription (no API key).";

/** Exact actionable message emitted as `fatal` when the SDK cannot be resolved. */
export const INSTALL_HINT =
  `Claude Agent SDK not found. Install it (npm install -g ${SDK_PACKAGE}) or ` +
  'set CLAUDE_AGENT_SDK_PATH to the node_modules directory containing it.';

/** How long to keep draining after an interrupt before closing the query. */
export const DRAIN_GRACE_MS = 5000;

/**
 * Instruction appended after the serialized transcript. Kept on one line on
 * purpose: a hard-wrapped sentence in the middle of a prompt reads as two
 * paragraphs to the model.
 */
export const CONTINUE_INSTRUCTION =
  'The transcript above is the prior conversation. Continue it: produce the next ' +
  'assistant response (after the last USER message or TOOL_RESULT). Use the ' +
  'provided tools when needed.';

/** Opening/closing markers of the serialized conversation. */
const TRANSCRIPT_OPEN = '<conversation_transcript>';
const TRANSCRIPT_CLOSE = '</conversation_transcript>';

/** Matches a `data:<mime>;base64,<payload>` URL (the only image form we inline). */
const DATA_URL_RE = /^data:([^;,]+);base64,(.*)$/s;

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

function readJsonFile(file) {
  try {
    return JSON.parse(readFileSync(file, 'utf8'));
  } catch {
    return null;
  }
}

function isObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Parse a data URL into `{mediaType, data}`, or null when it is not one. */
export function parseDataUrl(url) {
  if (typeof url !== 'string') return null;
  const match = DATA_URL_RE.exec(url.trim());
  if (!match) return null;
  return { mediaType: match[1], data: match[2] };
}

/** Remove the `mcp__lele__` namespace the SDK adds to lele's own tools. */
export function stripToolPrefix(name) {
  if (typeof name !== 'string') return '';
  return name.startsWith(MCP_TOOL_PREFIX) ? name.slice(MCP_TOOL_PREFIX.length) : name;
}

/**
 * Normalize tool arguments to an object. lele sends an object, but the
 * OpenAI-compatible wire shape carries a JSON *string*, and a malformed string
 * must not throw while building a transcript.
 */
export function normalizeArguments(value) {
  if (value === undefined || value === null) return {};
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) return {};
    try {
      const parsed = JSON.parse(trimmed);
      return isObject(parsed) ? parsed : { value: parsed };
    } catch {
      return { _raw: value };
    }
  }
  return value;
}

/** JSON with object keys sorted, so two equal argument maps compare equal. */
function stableJson(value) {
  if (Array.isArray(value)) return `[${value.map((item) => stableJson(item)).join(',')}]`;
  if (isObject(value)) {
    const keys = Object.keys(value).sort();
    return `{${keys.map((key) => `${JSON.stringify(key)}:${stableJson(value[key])}`).join(',')}}`;
  }
  const encoded = JSON.stringify(value);
  return encoded === undefined ? 'null' : encoded;
}

function toNumber(value) {
  return typeof value === 'number' && Number.isFinite(value) ? Math.trunc(value) : 0;
}

// ---------------------------------------------------------------------------
// SDK / MCP resolution
// ---------------------------------------------------------------------------

/**
 * Walk up from `startPath` looking for a package.json whose `name` is the SDK.
 * Returns `{dir, json}` or null. A few levels are enough (bin dir, package dir,
 * @scope dir, node_modules dir, ...) and the loop is bounded defensively.
 */
function findSdkPackage(startPath) {
  let dir = startPath;
  for (let depth = 0; depth < 12 && dir; depth += 1) {
    const json = readJsonFile(path.join(dir, 'package.json'));
    if (json && json.name === SDK_PACKAGE) return { dir, json };
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
}

/**
 * Resolve the ESM entry file declared by the package: `exports['.']` (string,
 * or object with default/import/require conditions), then `module`/`main`,
 * then the SDK's historical entry name.
 */
export function resolveEntryFile(pkgDir, pkgJson) {
  const exportsField = pkgJson?.exports;
  let entry;
  if (typeof exportsField === 'string') {
    entry = exportsField;
  } else if (isObject(exportsField)) {
    const root = isObject(exportsField['.']) ? exportsField['.'] : exportsField;
    if (typeof root === 'string') {
      entry = root;
    } else if (isObject(root)) {
      entry = root.default ?? root.import ?? root.node ?? root.require;
    }
  }
  if (typeof entry !== 'string' || entry === '') {
    entry = pkgJson?.module ?? pkgJson?.main ?? 'sdk.mjs';
  }
  return path.resolve(pkgDir, entry);
}

/**
 * Locate the SDK through the standard CJS resolver (honors NODE_PATH). Used to
 * read the version when the bare ESM import already succeeded.
 */
function locateResolvedSdk() {
  try {
    const require = createRequire(import.meta.url);
    const entry = require.resolve(SDK_PACKAGE);
    return findSdkPackage(path.dirname(entry));
  } catch {
    return null;
  }
}

/**
 * Resolve the MCP SDK modules the Agent SDK depends on.
 *
 * `createRequire` is anchored at the SDK's own package.json so the lookup uses
 * the SDK's dependency tree (the MCP package is a dependency of the Agent SDK,
 * not of lele). Only paths are resolved here — the modules are imported by
 * `loadMcpModules`, i.e. only when a chat actually exposes tools.
 *
 * Returns `{server, types}` (absolute file paths) or null when unavailable.
 */
export function resolveMcpPaths(sdkPackageJsonPath) {
  if (!sdkPackageJsonPath) return null;
  let require;
  try {
    require = createRequire(sdkPackageJsonPath);
  } catch {
    return null;
  }
  const resolveOne = (candidates) => {
    for (const candidate of candidates) {
      try {
        return require.resolve(candidate);
      } catch {
        // try the next spelling
      }
    }
    return '';
  };
  const server = resolveOne([
    '@modelcontextprotocol/sdk/server/index.js',
    '@modelcontextprotocol/sdk/server',
  ]);
  const types = resolveOne([
    '@modelcontextprotocol/sdk/types.js',
    '@modelcontextprotocol/sdk/types',
  ]);
  if (!server || !types) return null;
  return { server, types };
}

/**
 * Map a CJS build path (`dist/cjs/...`) to its ESM sibling when that exists.
 * Importing the ESM build keeps a single instance of the MCP SDK in the
 * process, which is what the Agent SDK itself would load.
 */
function esmSibling(filePath) {
  const marker = `${path.sep}cjs${path.sep}`;
  const index = filePath.lastIndexOf(marker);
  if (index < 0) return '';
  const candidate = `${filePath.slice(0, index)}${path.sep}esm${path.sep}${filePath.slice(index + marker.length)}`;
  return existsSync(candidate) ? candidate : '';
}

async function importPreferEsm(filePath) {
  const esm = esmSibling(filePath);
  if (esm) {
    try {
      return await import(pathToFileURL(esm).href);
    } catch {
      // fall through to the CJS build
    }
  }
  return import(pathToFileURL(filePath).href);
}

/** Named export from an ESM module or from a dynamically imported CJS module. */
function pickExport(module, name) {
  return module?.[name] ?? module?.default?.[name];
}

/**
 * Import the low-level MCP server pieces. Called lazily by the bridge, only
 * when a chat exposes at least one tool.
 */
export async function loadMcpModules(mcpPaths) {
  if (!mcpPaths) throw new Error('MCP SDK paths were not resolved');
  const [serverModule, typesModule] = await Promise.all([
    importPreferEsm(mcpPaths.server),
    importPreferEsm(mcpPaths.types),
  ]);
  const Server = pickExport(serverModule, 'Server');
  const ListToolsRequestSchema = pickExport(typesModule, 'ListToolsRequestSchema');
  const CallToolRequestSchema = pickExport(typesModule, 'CallToolRequestSchema');
  if (typeof Server !== 'function' || !ListToolsRequestSchema || !CallToolRequestSchema) {
    throw new Error('the installed @modelcontextprotocol/sdk does not export the low-level Server API');
  }
  return { Server, ListToolsRequestSchema, CallToolRequestSchema };
}

/**
 * Resolve the Claude Agent SDK.
 *
 * `searchPaths` are candidate `node_modules` directories (or the SDK package
 * directory itself), supplied by Go: CLAUDE_AGENT_SDK_PATH, `npm root -g` and
 * the usual global locations.
 *
 * Order: bare specifier first (works when the module is resolvable from this
 * file or via NODE_PATH), then each search path. Throws the actionable
 * INSTALL_HINT message when nothing resolves.
 *
 * Returns `{sdk, version, packageDir, packageJsonPath, mcp}` where `mcp` is the
 * resolved MCP SDK paths (or null) and `sdk.query` is the SDK entry point.
 */
export async function resolveSdk(searchPaths = []) {
  const candidates = [...(Array.isArray(searchPaths) ? searchPaths : [])];
  // NODE_PATH is only used as a fallback: Go passes the real search paths, but
  // a hand-started bridge should still find a globally installed SDK. (A bare
  // ESM `import()` does NOT honor NODE_PATH, which is why this exists.)
  const nodePath = process.env.NODE_PATH;
  if (typeof nodePath === 'string' && nodePath !== '') {
    candidates.push(...nodePath.split(path.delimiter));
  }

  try {
    const sdk = await import(SDK_PACKAGE);
    const located = locateResolvedSdk();
    return {
      sdk,
      version: located?.json?.version ?? 'unknown',
      packageDir: located?.dir ?? '',
      packageJsonPath: located ? path.join(located.dir, 'package.json') : '',
      mcp: located ? resolveMcpPaths(path.join(located.dir, 'package.json')) : null,
    };
  } catch {
    // not resolvable as a bare specifier — fall through to the search paths
  }

  for (const raw of candidates) {
    if (typeof raw !== 'string' || raw.trim() === '') continue;
    const base = path.resolve(raw.trim());
    const packageDirs = [path.join(base, SDK_PACKAGE), base];
    for (const dir of packageDirs) {
      const json = readJsonFile(path.join(dir, 'package.json'));
      if (!json || json.name !== SDK_PACKAGE) continue;
      const entry = resolveEntryFile(dir, json);
      const sdk = await import(pathToFileURL(entry).href);
      const packageJsonPath = path.join(dir, 'package.json');
      return {
        sdk,
        version: typeof json.version === 'string' ? json.version : 'unknown',
        packageDir: dir,
        packageJsonPath,
        mcp: resolveMcpPaths(packageJsonPath),
      };
    }
  }

  throw new Error(INSTALL_HINT);
}

// ---------------------------------------------------------------------------
// transcript / prompt construction
// ---------------------------------------------------------------------------

/**
 * Render one message's content.
 *
 * lele's Go `Message.MarshalJSON` folds `content_parts` into `content` (an
 * array of parts) when parts are present, so both spellings are accepted here:
 * `content` as an array, or a separate `content_parts` array.
 *
 * Data-URL images become real image blocks only when `allowImages` is set (the
 * LAST user message, see buildTranscript); everywhere else they degrade to the
 * `[image: <mime or url>]` placeholder, as do remote image URLs.
 *
 * Returns `{text, images}` with `images` as `[{mediaType, data}]`.
 */
function renderContent(message, allowImages) {
  const parts = Array.isArray(message?.content)
    ? message.content
    : Array.isArray(message?.content_parts) && message.content_parts.length > 0
      ? message.content_parts
      : null;

  if (!parts) {
    return {
      text: typeof message?.content === 'string' ? message.content : '',
      images: [],
    };
  }

  const chunks = [];
  const images = [];
  for (const part of parts) {
    if (!isObject(part)) continue;
    switch (part.type) {
      case 'text':
        if (typeof part.text === 'string') chunks.push(part.text);
        break;
      case 'image_url': {
        const url = part.image_url?.url ?? '';
        const inline = parseDataUrl(url);
        if (inline && allowImages) {
          images.push(inline);
          break;
        }
        chunks.push(`[image: ${inline ? inline.mediaType : url || 'unknown'}]`);
        break;
      }
      case 'video_url':
        chunks.push('[video omitted]');
        break;
      default:
        // Unknown part type: keep any text it carries rather than losing it.
        if (typeof part.text === 'string') chunks.push(part.text);
        break;
    }
  }
  return { text: chunks.join(''), images };
}

/**
 * Normalize one tool call, accepting both wire shapes:
 * `{function: {name, arguments}}` (arguments = JSON string) and the flat
 * `{name, arguments}` (arguments = object).
 */
export function normalizeToolCall(toolCall) {
  const fn = isObject(toolCall?.function) ? toolCall.function : null;
  const name = fn && typeof fn.name === 'string' ? fn.name : typeof toolCall?.name === 'string' ? toolCall.name : '';
  const rawArguments = fn && fn.arguments !== undefined ? fn.arguments : toolCall?.arguments;
  return {
    id: typeof toolCall?.id === 'string' ? toolCall.id : '',
    name,
    arguments: normalizeArguments(rawArguments),
  };
}

/** Normalize a `tool_calls` array, dropping entries that carry no name. */
export function normalizeToolCalls(toolCalls) {
  if (!Array.isArray(toolCalls)) return [];
  const normalized = [];
  for (const toolCall of toolCalls) {
    const call = normalizeToolCall(toolCall);
    if (!call.name) continue;
    normalized.push(call);
  }
  return normalized;
}

/**
 * Serialize lele's conversation into the single prompt sent to Claude Code.
 *
 * - A lone user message is passed through verbatim (no wrapper): it is the
 *   common case and the wrapper would only add noise.
 * - Anything longer becomes a `<conversation_transcript>` block with one
 *   section per message, followed by CONTINUE_INSTRUCTION.
 * - Data-URL images in the LAST user message are returned separately in
 *   `images` so the bridge can send real image blocks; the same images earlier
 *   in the history stay as placeholders.
 *
 * Returns `{text, images}`.
 */
export function buildTranscript(messages = []) {
  const list = Array.isArray(messages) ? messages : [];
  if (list.length === 0) return { text: '', images: [] };

  const lastIndex = list.length - 1;
  const roleOf = (message) => String(message?.role ?? '').toLowerCase();

  if (list.length === 1 && roleOf(list[0]) === 'user') {
    const rendered = renderContent(list[0], true);
    return { text: rendered.text, images: rendered.images };
  }

  const lines = [TRANSCRIPT_OPEN];
  const images = [];

  for (let index = 0; index < list.length; index += 1) {
    const message = isObject(list[index]) ? list[index] : {};
    const role = roleOf(message);
    const allowImages = index === lastIndex && role === 'user';
    const rendered = renderContent(message, allowImages);
    if (allowImages && rendered.images.length > 0) images.push(...rendered.images);

    if (role === 'assistant') {
      lines.push('ASSISTANT:');
      if (rendered.text) lines.push(rendered.text);
      for (const call of normalizeToolCalls(message.tool_calls)) {
        lines.push(
          `ASSISTANT_TOOL_CALL id=${call.id} name=${call.name} arguments=${stableJson(call.arguments)}`,
        );
      }
    } else if (role === 'tool') {
      lines.push(`TOOL_RESULT id=${typeof message.tool_call_id === 'string' ? message.tool_call_id : ''}:`);
      if (rendered.text) lines.push(rendered.text);
    } else {
      // user, and any unexpected role: label it and keep the content.
      lines.push(`${role.toUpperCase()}:`);
      if (rendered.text) lines.push(rendered.text);
    }
  }

  lines.push(TRANSCRIPT_CLOSE, '', CONTINUE_INSTRUCTION);
  return { text: lines.join('\n'), images };
}

/**
 * One-shot async iterable yielding a single SDK user message carrying the
 * prompt text plus real base64 image blocks (streaming-input shape).
 */
export async function* singleUserMessage(text, images = []) {
  const content = [{ type: 'text', text }];
  for (const image of images) {
    content.push({
      type: 'image',
      source: { type: 'base64', media_type: image.mediaType, data: image.data },
    });
  }
  yield {
    type: 'user',
    message: { role: 'user', content },
    parent_tool_use_id: null,
    session_id: '',
  };
}

/**
 * Turn a built transcript into what `query()` accepts: the plain string when
 * there are no inline images, an async iterable of one SDKUserMessage when
 * there are.
 */
export function buildPrompt(transcript) {
  const text = typeof transcript?.text === 'string' ? transcript.text : '';
  const images = Array.isArray(transcript?.images) ? transcript.images : [];
  if (images.length === 0) return text;
  return singleUserMessage(text, images);
}

// ---------------------------------------------------------------------------
// query options
// ---------------------------------------------------------------------------

/** lele's provider sentinel model name — it must not be forwarded to the SDK. */
const PROVIDER_MODEL_SENTINEL = 'claude-code';

/**
 * Map lele's model name to the SDK's `model` option. `''` and the provider
 * sentinel `claude-code` both mean "let Claude Code pick its default".
 */
export function normalizeModel(model) {
  const value = typeof model === 'string' ? model.trim() : '';
  if (value === '' || value === PROVIDER_MODEL_SENTINEL) return undefined;
  return value;
}

/**
 * Build the `query()` options for one chat turn.
 *
 * Invariants (see the spec):
 * - `tools: []` — every built-in Claude Code tool is disabled; lele's own tools
 *   arrive through the in-process `lele` MCP server instead.
 * - `settingSources: []`, `persistSession: false` — no CLAUDE.md, no user
 *   settings, no session files: each turn is a stateless replay.
 * - `permissionMode: 'default'` plus a denying `canUseTool`, so nothing is ever
 *   executed inside Claude Code even if the interrupt loses a race.
 *
 * `mcpServer` is the `{type:'sdk', name, instance}` entry to expose; it is only
 * wired up when the chat actually has tools.
 */
export function buildQueryOptions(options = {}) {
  const {
    systemPrompt = '',
    tools = [],
    mcpServer = null,
    model = '',
    stream = false,
    claudePath = '',
    abortController,
    canUseTool,
    stderr,
    env,
  } = options;

  const toolCount = Array.isArray(tools) ? tools.length : 0;
  const mcpServers = toolCount > 0 && mcpServer ? { [MCP_SERVER_NAME]: mcpServer } : {};

  return {
    systemPrompt: systemPrompt || undefined,
    tools: [],
    mcpServers,
    allowedTools: [],
    permissionMode: 'default',
    canUseTool,
    settingSources: [],
    persistSession: false,
    includePartialMessages: Boolean(stream),
    model: normalizeModel(model),
    pathToClaudeCodeExecutable: claudePath || undefined,
    abortController,
    stderr,
    env: env ?? process.env,
  };
}

// ---------------------------------------------------------------------------
// per-request state machine (capture + interrupt + result assembly)
// ---------------------------------------------------------------------------

/**
 * Accumulates one chat turn from the SDK message stream.
 *
 * Deliberately clock-free apart from the injectable `now`, so the interrupt /
 * drain / result logic is unit-testable: the bridge owns the timers and simply
 * asks `drainDeadline` when to force `q.close()`.
 *
 * Tool calls are recorded from two sources and deduplicated:
 * - `canUseTool(name, input, {toolUseID})` may fire first, and on SDK versions
 *   that do not pass `toolUseID` it is recorded without an id;
 * - the assistant message's `tool_use` blocks are authoritative and carry the
 *   id. Whichever source reports the call first wins; the other one is merged
 *   into it (an id recorded late is filled in, never duplicated).
 */
export class QuerySession {
  constructor({ stream = false, drainGraceMs = DRAIN_GRACE_MS, now = Date.now } = {}) {
    this.stream = Boolean(stream);
    this.drainGraceMs = drainGraceMs;
    this.now = now;

    /** Text from assistant `text` blocks — the authoritative transcript. */
    this.finalText = '';
    /** Text from `text_delta` stream events (fallback when no block arrived). */
    this.streamedText = '';
    /** Reasoning from assistant `thinking` blocks. */
    this.finalReasoning = '';
    /** Reasoning from `thinking_delta` stream events (fallback). */
    this.streamedReasoning = '';

    /** Captured calls: `[{id, name, arguments, _sig}]` in first-seen order. */
    this.toolCalls = [];

    /** True once at least one tool_use was captured. */
    this.captured = false;
    /** True once `q.interrupt()` was requested for this turn. */
    this.interrupted = false;
    /** Epoch ms after which the bridge should close the query (0 = no drain). */
    this.drainDeadline = 0;

    this.resultSeen = false;
    this.resultSubtype = '';
    this.resultText = '';
    this.isError = false;
    this.errors = [];
    this.resultUsage = null;
    this.lastAssistantUsage = null;
  }

  /**
   * Record one tool call. Returns the stored entry.
   *
   * Dedupe rules:
   * 1. Same id already stored -> return it (keep the first recording).
   * 2. Same name+arguments and the stored entry has no id yet -> attach the id
   *    to it (canUseTool fired first without a `toolUseID`).
   * 3. Same name+arguments and the stored entry already has an id -> drop this
   *    duplicate (a `canUseTool` call for a block already recorded).
   * 4. Otherwise append.
   */
  recordToolUse(name, input, id) {
    const cleanName = stripToolPrefix(name);
    const args = normalizeArguments(input);
    const callId = typeof id === 'string' && id !== '' ? id : '';
    const signature = `${cleanName}\u0000${stableJson(args)}`;

    if (callId) {
      const byId = this.toolCalls.find((call) => call.id === callId);
      if (byId) return byId;
    }

    // Same arguments: either the same call reported twice (merge / drop) or two
    // genuinely identical calls that must both be kept when both carry an id.
    const bySignature = callId
      ? this.toolCalls.find((call) => call._sig === signature && !call.id)
      : this.toolCalls.find((call) => call._sig === signature);
    if (bySignature) {
      if (callId && !bySignature.id) bySignature.id = callId;
      return bySignature;
    }

    const entry = { id: callId, name: cleanName, arguments: args, _sig: signature };
    this.toolCalls.push(entry);
    return entry;
  }

  /**
   * Fold one SDK message into the state.
   *
   * Returns the side effects the bridge must apply:
   * `{chunks, reasoning, interrupt, done}` — `interrupt` is set once, on the
   * first assistant message that carries a tool_use; `done` on the result
   * message (the turn-complete signal).
   */
  handleMessage(message) {
    const effects = { chunks: [], reasoning: [], interrupt: false, done: false };
    if (!isObject(message)) return effects;

    switch (message.type) {
      case 'stream_event': {
        if (!this.stream) break;
        const event = isObject(message.event) ? message.event : {};
        if (event.type !== 'content_block_delta') break;
        const delta = isObject(event.delta) ? event.delta : {};
        if (delta.type === 'text_delta' && typeof delta.text === 'string' && delta.text !== '') {
          this.streamedText += delta.text;
          effects.chunks.push(delta.text);
        } else if (
          delta.type === 'thinking_delta' &&
          typeof delta.thinking === 'string' &&
          delta.thinking !== ''
        ) {
          this.streamedReasoning += delta.thinking;
          effects.reasoning.push(delta.thinking);
        }
        break;
      }

      case 'assistant': {
        // Subagent frames (no subagents are possible with tools: [], but a
        // frame must never be mistaken for the main turn's output).
        if (message.parent_tool_use_id) break;
        const inner = isObject(message.message) ? message.message : {};
        if (isObject(inner.usage)) this.lastAssistantUsage = inner.usage;

        const blocks = Array.isArray(inner.content) ? inner.content : [];
        let sawToolUse = false;
        for (const block of blocks) {
          if (!isObject(block)) continue;
          if (block.type === 'text' && typeof block.text === 'string') {
            this.finalText += block.text;
          } else if (block.type === 'thinking' && typeof block.thinking === 'string') {
            this.finalReasoning += block.thinking;
          } else if (block.type === 'tool_use') {
            this.recordToolUse(block.name, block.input, block.id);
            sawToolUse = true;
          }
        }

        if (sawToolUse) {
          this.captured = true;
          if (!this.interrupted) {
            this.interrupted = true;
            effects.interrupt = true;
            this.drainDeadline = this.now() + this.drainGraceMs;
          }
        }
        break;
      }

      case 'result': {
        this.resultSeen = true;
        this.resultSubtype = typeof message.subtype === 'string' ? message.subtype : '';
        this.isError =
          message.is_error === true ||
          (this.resultSubtype !== '' && this.resultSubtype !== 'success');
        if (Array.isArray(message.errors)) {
          this.errors = message.errors.filter((entry) => typeof entry === 'string' && entry !== '');
        }
        if (typeof message.result === 'string') this.resultText = message.result;
        if (isObject(message.usage)) this.resultUsage = message.usage;
        effects.done = true;
        break;
      }

      default:
        // system / user echoes / task notifications: nothing to accumulate.
        break;
    }

    return effects;
  }

  /** Usage to report: the result message wins, the last assistant message is the fallback. */
  usage() {
    return this.resultUsage ?? this.lastAssistantUsage;
  }

  /**
   * Assemble the `result` protocol event body.
   *
   * Content is the assistant text blocks (authoritative); on a normal stop the
   * CLI's `result.result` string is preferred, because it is exactly what
   * Claude Code considers the final answer.
   */
  resultPayload() {
    const hasToolCalls = this.toolCalls.length > 0;
    const content = hasToolCalls
      ? this.finalText || this.streamedText
      : this.resultText || this.finalText || this.streamedText;
    return {
      content,
      reasoning: this.finalReasoning || this.streamedReasoning,
      tool_calls: this.toolCalls.map((call) => ({
        id: call.id,
        name: call.name,
        arguments: call.arguments,
      })),
      finish_reason: hasToolCalls ? 'tool_calls' : 'stop',
      usage: mapUsage(this.usage()),
    };
  }

  /** Assemble the `error` protocol event body for a failed turn. */
  errorPayload() {
    const message =
      this.errors.length > 0
        ? this.errors.join('; ')
        : this.resultText ||
          `Claude Code query failed (${this.resultSubtype || 'no result message'})`;
    const errorClass = classifyError(message, this.resultSubtype);
    return { message: withAuthHint(message, errorClass), class: errorClass };
  }
}

/**
 * Map SDK usage (`input_tokens`, `output_tokens`, `cache_creation_input_tokens`,
 * `cache_read_input_tokens`) to the protocol shape. Returns null when there is
 * no usage at all, so the bridge can omit the field.
 */
export function mapUsage(raw) {
  if (!isObject(raw)) return null;
  const usage = {
    input_tokens: toNumber(raw.input_tokens),
    output_tokens: toNumber(raw.output_tokens),
    cache_creation_input_tokens: toNumber(raw.cache_creation_input_tokens),
    cache_read_input_tokens: toNumber(raw.cache_read_input_tokens),
  };
  const total =
    usage.input_tokens +
    usage.output_tokens +
    usage.cache_creation_input_tokens +
    usage.cache_read_input_tokens;
  return total === 0 ? null : usage;
}

// ---------------------------------------------------------------------------
// error classification
// ---------------------------------------------------------------------------

/**
 * Ordered classification table. The first match wins, so the more specific
 * patterns (auth, then throttling) must stay above the generic 4xx ones.
 */
const ERROR_PATTERNS = [
  { class: 'auth', pattern: /invalid api key|authentication|unauthorized|please run \/login|not logged in|oauth|credential/i },
  { class: 'rate_limit', pattern: /rate.?limit|429|too many requests|usage limit|quota/i },
  { class: 'overloaded', pattern: /overloaded|529|503|service unavailable/i },
  { class: 'timeout', pattern: /timed? ?out|etimedout|deadline/i },
  { class: 'format', pattern: /invalid request|400|malformed|schema/i },
];

/**
 * Classify a failure into the wire classes Go maps to failover reasons.
 * `subtype` is the SDK result subtype (e.g. `error_during_execution`) and is
 * scanned together with the message, because some failures only show up there.
 */
export function classifyError(error, subtype = '') {
  const message =
    typeof error === 'string'
      ? error
      : typeof error?.message === 'string'
        ? error.message
        : error === undefined || error === null
          ? ''
          : String(error);
  const haystack = `${subtype ?? ''} ${message}`;
  for (const entry of ERROR_PATTERNS) {
    if (entry.pattern.test(haystack)) return entry.class;
  }
  return 'unknown';
}

/** Append the login hint required for auth failures (idempotent). */
export function withAuthHint(message, errorClass) {
  const text = typeof message === 'string' ? message : String(message ?? '');
  if (errorClass !== 'auth' || text.includes(AUTH_HINT)) return text;
  return text ? `${text} ${AUTH_HINT}` : AUTH_HINT;
}

// ---------------------------------------------------------------------------
// MCP server exposing lele's tools
// ---------------------------------------------------------------------------

/**
 * Map lele's tool definitions (`{type:'function', function:{name, description,
 * parameters}}`) to MCP tool definitions. The JSON Schema is passed through
 * UNCHANGED — Claude Code validates arguments against it, so any rewriting
 * would silently change what the model is allowed to send.
 */
export function mapTools(tools = []) {
  const definitions = [];
  for (const tool of Array.isArray(tools) ? tools : []) {
    const fn = isObject(tool?.function) ? tool.function : tool;
    const name = typeof fn?.name === 'string' ? fn.name : '';
    if (!name) continue;
    const description = typeof fn?.description === 'string' ? fn.description : '';
    const parameters =
      isObject(fn?.parameters) && Object.keys(fn.parameters).length > 0
        ? fn.parameters
        : { type: 'object', properties: {} };
    definitions.push({ name, description, inputSchema: parameters });
  }
  return definitions;
}

/**
 * Build the in-process MCP server lele's tools are advertised through.
 *
 * The low-level `Server` is used on purpose: it accepts JSON Schema verbatim
 * (`createSdkMcpServer` would need a JSON-Schema→zod conversion and lose
 * fidelity). `CallTool` never executes anything — with `canUseTool` denying
 * every request it should not even be reached; it answers with the sentinel
 * text so a stray call cannot do damage.
 */
export function createLeleMcpServer({ Server, ListToolsRequestSchema, CallToolRequestSchema, tools = [] }) {
  if (typeof Server !== 'function') throw new Error('MCP Server class is required');
  if (!ListToolsRequestSchema || !CallToolRequestSchema) {
    throw new Error('MCP request schemas are required');
  }

  const server = new Server(
    { name: MCP_SERVER_NAME, version: MCP_SERVER_VERSION },
    { capabilities: { tools: {} } },
  );
  const definitions = mapTools(tools);

  server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools: definitions }));
  server.setRequestHandler(CallToolRequestSchema, async () => ({
    content: [{ type: 'text', text: CAPTURED_TOOL_TEXT }],
  }));

  return server;
}

// ---------------------------------------------------------------------------
// JSON-lines framing
// ---------------------------------------------------------------------------

/** Encode one protocol object as a newline-terminated JSON line. */
export function encodeLine(value) {
  return `${JSON.stringify(value)}\n`;
}

/**
 * Parse one request line. Returns null for blank lines; throws for anything
 * that is not a JSON object (the bridge logs and ignores those).
 */
export function parseRequestLine(line) {
  const trimmed = typeof line === 'string' ? line.trim() : '';
  if (trimmed === '') return null;
  const parsed = JSON.parse(trimmed);
  if (!isObject(parsed)) throw new Error('request line must be a JSON object');
  if (typeof parsed.type !== 'string' || parsed.type === '') {
    throw new Error('request line is missing a type');
  }
  return parsed;
}