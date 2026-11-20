// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

/**
 * Unit tests for the Claude Agent SDK bridge logic.
 *
 * Run with: node --test pkg/providers/claudecode/bridge_test.mjs
 *
 * They must pass WITHOUT @anthropic-ai/claude-agent-sdk installed: nothing
 * here imports the SDK, bridge_lib.mjs only imports it lazily, and the SDK
 * resolution test uses a throwaway fake package on disk.
 */

import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  AUTH_HINT,
  CAPTURED_TOOL_TEXT,
  CONTINUE_INSTRUCTION,
  DRAIN_GRACE_MS,
  INSTALL_HINT,
  MCP_TOOL_PREFIX,
  QuerySession,
  buildPrompt,
  buildQueryOptions,
  buildTranscript,
  classifyError,
  createLeleMcpServer,
  encodeLine,
  mapTools,
  mapUsage,
  normalizeArguments,
  normalizeModel,
  parseDataUrl,
  parseRequestLine,
  resolveEntryFile,
  resolveMcpPaths,
  resolveSdk,
  stripToolPrefix,
  withAuthHint,
} from './bridge_lib.mjs';

const DATA_IMAGE = `data:image/png;base64,${Buffer.from('fake-png').toString('base64')}`;

/** Build the minimal SDK assistant message the state machine consumes. */
function assistantMessage(blocks, extra = {}) {
  return { type: 'assistant', message: { content: blocks, ...extra } };
}

// ---------------------------------------------------------------------------
// buildTranscript
// ---------------------------------------------------------------------------

test('buildTranscript passes a single user message through verbatim', () => {
  const transcript = buildTranscript([{ role: 'user', content: 'hello there' }]);
  assert.equal(transcript.text, 'hello there');
  assert.deepEqual(transcript.images, []);
});

test('buildTranscript wraps a multi-turn conversation and appends the instruction', () => {
  const transcript = buildTranscript([
    { role: 'user', content: 'first question' },
    { role: 'assistant', content: 'first answer' },
    { role: 'user', content: 'second question' },
  ]);
  assert.equal(
    transcript.text,
    [
      '<conversation_transcript>',
      'USER:',
      'first question',
      'ASSISTANT:',
      'first answer',
      'USER:',
      'second question',
      '</conversation_transcript>',
      '',
      CONTINUE_INSTRUCTION,
    ].join('\n'),
  );
});

test('buildTranscript renders tool results with their call id', () => {
  const transcript = buildTranscript([
    { role: 'user', content: 'weather?' },
    { role: 'assistant', content: '', tool_calls: [{ id: 'call_1', name: 'get_weather', arguments: '{"city":"Paris"}' }] },
    { role: 'tool', tool_call_id: 'call_1', content: 'sunny' },
    { role: 'user', content: 'thanks' },
  ]);
  assert.match(
    transcript.text,
    /ASSISTANT:\nASSISTANT_TOOL_CALL id=call_1 name=get_weather arguments=\{"city":"Paris"\}/,
  );
  assert.match(transcript.text, /TOOL_RESULT id=call_1:\nsunny/);
});

test('buildTranscript supports the wire shape {function:{name,arguments}} (JSON string)', () => {
  const transcript = buildTranscript([
    { role: 'user', content: 'go' },
    {
      role: 'assistant',
      content: 'calling',
      tool_calls: [
        {
          id: 'tc_9',
          type: 'function',
          function: { name: 'read_file', arguments: '{"path":"a.txt","line":3}' },
        },
      ],
    },
    { role: 'tool', tool_call_id: 'tc_9', content: 'ok' },
    { role: 'user', content: 'done' },
  ]);
  assert.match(
    transcript.text,
    /ASSISTANT_TOOL_CALL id=tc_9 name=read_file arguments=\{"line":3,"path":"a\.txt"\}/,
  );
});

test('buildTranscript supports the flat wire shape {name,arguments} (object)', () => {
  const transcript = buildTranscript([
    { role: 'user', content: 'go' },
    { role: 'assistant', content: '', tool_calls: [{ id: 'tc_1', name: 'ping', arguments: { host: 'example.test' } }] },
    { role: 'tool', tool_call_id: 'tc_1', content: 'pong' },
    { role: 'user', content: 'done' },
  ]);
  assert.match(transcript.text, /ASSISTANT_TOOL_CALL id=tc_1 name=ping arguments=\{"host":"example\.test"\}/);
});

test('buildTranscript keeps every tool call of a content-less assistant turn', () => {
  const transcript = buildTranscript([
    { role: 'user', content: 'two calls' },
    {
      role: 'assistant',
      content: '',
      tool_calls: [
        { id: 'a', name: 'one', arguments: '{}' },
        { id: 'b', name: 'two', arguments: '{}' },
      ],
    },
    { role: 'tool', tool_call_id: 'a', content: '1' },
    { role: 'tool', tool_call_id: 'b', content: '2' },
    { role: 'user', content: 'go on' },
  ]);
  assert.match(transcript.text, /ASSISTANT:\nASSISTANT_TOOL_CALL id=a name=one arguments=\{\}\nASSISTANT_TOOL_CALL id=b name=two arguments=\{\}/);
});

test('buildTranscript degrades images in history to placeholders', () => {
  const transcript = buildTranscript([
    {
      role: 'user',
      content: [
        { type: 'text', text: 'look at this' },
        { type: 'image_url', image_url: { url: DATA_IMAGE } },
      ],
    },
    { role: 'assistant', content: 'nice picture' },
    {
      role: 'user',
      content: [{ type: 'text', text: 'and now?' }],
    },
  ]);
  assert.match(transcript.text, /\[image: image\/png\]/);
  assert.deepEqual(transcript.images, []);
});

test('buildTranscript returns data-URL images of the last user message as content parts', async () => {
  const transcript = buildTranscript([
    { role: 'user', content: 'older turn' },
    { role: 'assistant', content: 'ok' },
    {
      role: 'user',
      content: [
        { type: 'text', text: 'what is this?' },
        { type: 'image_url', image_url: { url: DATA_IMAGE } },
      ],
    },
  ]);
  assert.equal(transcript.images.length, 1);
  assert.equal(transcript.images[0].mediaType, 'image/png');
  assert.match(transcript.text, /what is this\?/);
  assert.doesNotMatch(transcript.text, /\[image:/);

  const prompt = buildPrompt(transcript);
  assert.equal(typeof prompt?.[Symbol.asyncIterator], 'function');
  const chunks = [];
  for await (const message of prompt) chunks.push(message);
  assert.equal(chunks.length, 1);
  assert.deepEqual(chunks[0].message.content, [
    { type: 'text', text: transcript.text },
    {
      type: 'image',
      source: { type: 'base64', media_type: 'image/png', data: transcript.images[0].data },
    },
  ]);
});

test('buildTranscript handles data-URL images in a lone user message', async () => {
  const transcript = buildTranscript([
    { role: 'user', content: [{ type: 'text', text: 'describe' }, { type: 'image_url', image_url: { url: DATA_IMAGE } }] },
  ]);
  assert.equal(transcript.text, 'describe');
  assert.equal(transcript.images.length, 1);
});

test('buildTranscript renders video parts as an omission placeholder', () => {
  const transcript = buildTranscript([
    {
      role: 'user',
      content: [
        { type: 'text', text: 'video:' },
        { type: 'video_url', video_url: { url: 'https://example.test/v.mp4' } },
      ],
    },
    { role: 'assistant', content: 'seen' },
    { role: 'user', content: 'next' },
  ]);
  assert.match(transcript.text, /video:\[video omitted\]/);
});

test('buildTranscript accepts content_parts as a separate field', () => {
  const transcript = buildTranscript([
    { role: 'user', content: '', content_parts: [{ type: 'text', text: 'from parts' }] },
    { role: 'assistant', content: 'ok' },
    { role: 'user', content: 'end' },
  ]);
  assert.match(transcript.text, /USER:\nfrom parts/);
});

test('buildPrompt returns a plain string when there are no images', () => {
  const prompt = buildPrompt(buildTranscript([{ role: 'user', content: 'plain' }]));
  assert.equal(prompt, 'plain');
});// ---------------------------------------------------------------------------
// buildQueryOptions / normalizeModel
// ---------------------------------------------------------------------------

test('normalizeModel maps the provider sentinel to undefined', () => {
  assert.equal(normalizeModel(''), undefined);
  assert.equal(normalizeModel('   '), undefined);
  assert.equal(normalizeModel('claude-code'), undefined);
  assert.equal(normalizeModel('claude-sonnet-4-5'), 'claude-sonnet-4-5');
  assert.equal(normalizeModel(undefined), undefined);
});

test('buildQueryOptions disables built-in tools and settings leakage', () => {
  const options = buildQueryOptions({
    systemPrompt: 'You are lele.',
    model: 'claude-code',
    stream: true,
    claudePath: '/usr/local/bin/claude',
  });

  assert.deepEqual(options.tools, []);
  assert.deepEqual(options.allowedTools, []);
  assert.deepEqual(options.settingSources, []);
  assert.equal(options.persistSession, false);
  assert.equal(options.permissionMode, 'default');
  assert.equal(options.includePartialMessages, true);
  assert.equal(options.systemPrompt, 'You are lele.');
  assert.equal(options.model, undefined);
  assert.equal(options.pathToClaudeCodeExecutable, '/usr/local/bin/claude');
  assert.deepEqual(options.mcpServers, {});
  assert.equal(options.env, process.env);
});

test('buildQueryOptions leaves systemPrompt and claude path undefined when unset', () => {
  const options = buildQueryOptions({ stream: false });
  assert.equal(options.systemPrompt, undefined);
  assert.equal(options.pathToClaudeCodeExecutable, undefined);
  assert.equal(options.includePartialMessages, false);
});

test('buildQueryOptions forwards the model and only wires mcpServers when tools exist', () => {
  const server = { type: 'sdk', name: 'lele', instance: { fake: true } };

  const withoutTools = buildQueryOptions({ tools: [], mcpServer: server, model: 'claude-opus-4-5' });
  assert.equal(withoutTools.model, 'claude-opus-4-5');
  assert.deepEqual(withoutTools.mcpServers, {});

  const withTools = buildQueryOptions({
    tools: [{ type: 'function', function: { name: 'ping', parameters: {} } }],
    mcpServer: server,
  });
  assert.deepEqual(withTools.mcpServers, { lele: server });
});

test('buildQueryOptions passes the canUseTool and stderr callbacks through', () => {
  const canUseTool = async () => ({ behavior: 'deny', message: 'no' });
  const stderr = () => {};
  const options = buildQueryOptions({ canUseTool, stderr });
  assert.equal(options.canUseTool, canUseTool);
  assert.equal(options.stderr, stderr);
});

// ---------------------------------------------------------------------------
// capture state machine
// ---------------------------------------------------------------------------

test('QuerySession strips the mcp__lele__ prefix from captured tool names', () => {
  const session = new QuerySession();
  session.handleMessage(assistantMessage([{ type: 'tool_use', id: 'tu_1', name: `${MCP_TOOL_PREFIX}get_weather`, input: { city: 'Paris' } }]));

  assert.equal(session.toolCalls.length, 1);
  assert.equal(session.toolCalls[0].name, 'get_weather');
  assert.equal(stripToolPrefix(`${MCP_TOOL_PREFIX}get_weather`), 'get_weather');
  assert.equal(stripToolPrefix('other_tool'), 'other_tool');
});

test('QuerySession dedupes by tool_use id and keeps the first recording', () => {
  const session = new QuerySession();
  session.recordToolUse('get_weather', { city: 'Paris' }, 'tu_1');
  session.handleMessage(assistantMessage([{ type: 'tool_use', id: 'tu_1', name: 'get_weather', input: { city: 'Paris' } }]));
  session.recordToolUse('get_weather', { city: 'Paris' }, 'tu_1');

  assert.equal(session.toolCalls.length, 1);
  assert.equal(session.toolCalls[0].id, 'tu_1');
});

test('QuerySession merges a canUseTool-first capture (no id) with the assistant block', () => {
  const session = new QuerySession();
  // Older SDK versions call canUseTool without a toolUseID.
  session.recordToolUse(`${MCP_TOOL_PREFIX}get_weather`, { city: 'Paris' });
  session.handleMessage(assistantMessage([{ type: 'tool_use', id: 'tu_7', name: 'get_weather', input: { city: 'Paris' } }]));

  assert.equal(session.toolCalls.length, 1);
  assert.equal(session.toolCalls[0].id, 'tu_7');
  assert.equal(session.toolCalls[0].name, 'get_weather');
});

test('QuerySession keeps two identical calls that carry different ids', () => {
  const session = new QuerySession();
  session.handleMessage(
    assistantMessage([
      { type: 'tool_use', id: 'tu_a', name: 'ping', input: { host: 'x' } },
      { type: 'tool_use', id: 'tu_b', name: 'ping', input: { host: 'x' } },
    ]),
  );
  assert.equal(session.toolCalls.length, 2);
  assert.deepEqual(session.toolCalls.map((call) => call.id), ['tu_a', 'tu_b']);
});

test('QuerySession records calls in first-seen order and only interrupts once', () => {
  let clock = 0;
  const session = new QuerySession({ drainGraceMs: 5000, now: () => clock });

  const first = session.handleMessage(assistantMessage([{ type: 'tool_use', id: 'tu_1', name: 'one', input: {} }]));
  assert.equal(first.interrupt, true);
  assert.equal(session.drainDeadline, DRAIN_GRACE_MS);

  clock = 1000;
  const second = session.handleMessage(assistantMessage([{ type: 'tool_use', id: 'tu_2', name: 'two', input: {} }]));
  assert.equal(second.interrupt, false);
  assert.equal(session.drainDeadline, DRAIN_GRACE_MS);

  assert.deepEqual(session.toolCalls.map((call) => call.id), ['tu_1', 'tu_2']);
});

test('QuerySession does not interrupt on text-only assistant messages', () => {
  const session = new QuerySession();
  const effects = session.handleMessage(assistantMessage([{ type: 'text', text: 'just talking' }]));
  assert.equal(effects.interrupt, false);
  assert.equal(session.finalText, 'just talking');
  assert.equal(session.captured, false);
});

test('QuerySession assembles a tool_calls result payload', () => {
  const session = new QuerySession();
  session.handleMessage(assistantMessage([{ type: 'text', text: 'Let me check' }]));
  session.handleMessage(
    assistantMessage([{ type: 'tool_use', id: 'tu_1', name: `${MCP_TOOL_PREFIX}get_weather`, input: { city: 'Paris' } }]),
  );
  session.handleMessage({
    type: 'result',
    subtype: 'success',
    is_error: false,
    result: 'Let me check',
    usage: { input_tokens: 10, output_tokens: 5, cache_creation_input_tokens: 2, cache_read_input_tokens: 3 },
  });

  const payload = session.resultPayload();
  assert.equal(payload.finish_reason, 'tool_calls');
  assert.equal(payload.content, 'Let me check');
  assert.deepEqual(payload.tool_calls, [{ id: 'tu_1', name: 'get_weather', arguments: { city: 'Paris' } }]);
  assert.deepEqual(payload.usage, {
    input_tokens: 10,
    output_tokens: 5,
    cache_creation_input_tokens: 2,
    cache_read_input_tokens: 3,
  });
});

test('QuerySession prefers result.result on a normal stop and falls back to blocks', () => {
  const stopped = new QuerySession();
  stopped.handleMessage(assistantMessage([{ type: 'text', text: 'from blocks' }]));
  stopped.handleMessage({ type: 'result', subtype: 'success', result: 'from result' });
  assert.equal(stopped.resultPayload().finish_reason, 'stop');
  assert.equal(stopped.resultPayload().content, 'from result');

  const noResultText = new QuerySession();
  noResultText.handleMessage(assistantMessage([{ type: 'text', text: 'from blocks' }]));
  noResultText.handleMessage({ type: 'result', subtype: 'success' });
  assert.equal(noResultText.resultPayload().content, 'from blocks');
});

test('QuerySession streams deltas only when stream is enabled', () => {
  const streamEvent = (delta) => ({ type: 'stream_event', event: { type: 'content_block_delta', delta } });

  const streaming = new QuerySession({ stream: true });
  const textEffects = streaming.handleMessage(streamEvent({ type: 'text_delta', text: 'he' }));
  const thinkingEffects = streaming.handleMessage(streamEvent({ type: 'thinking_delta', thinking: 'hmm' }));
  assert.deepEqual(textEffects.chunks, ['he']);
  assert.deepEqual(thinkingEffects.reasoning, ['hmm']);

  const silent = new QuerySession({ stream: false });
  const silentEffects = silent.handleMessage(streamEvent({ type: 'text_delta', text: 'ignored' }));
  assert.deepEqual(silentEffects.chunks, []);
  assert.deepEqual(silentEffects.reasoning, []);
});

test('QuerySession ignores subagent frames', () => {
  const session = new QuerySession();
  session.handleMessage({
    type: 'assistant',
    parent_tool_use_id: 'tool_1',
    message: {
      content: [
        { type: 'text', text: 'subagent noise' },
        { type: 'tool_use', id: 'tu_sub', name: 'Bash', input: {} },
      ],
    },
  });
  assert.equal(session.finalText, '');
  assert.equal(session.toolCalls.length, 0);
  assert.equal(session.interrupted, false);
});

test('QuerySession marks error results and builds a classified error payload', () => {
  const session = new QuerySession();
  session.handleMessage({
    type: 'result',
    subtype: 'error_during_execution',
    is_error: true,
    errors: ['Invalid API key · Please run /login'],
  });

  assert.equal(session.isError, true);
  const payload = session.errorPayload();
  assert.equal(payload.class, 'auth');
  assert.match(payload.message, /Invalid API key/);
  assert.match(payload.message, new RegExp(AUTH_HINT.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
});

test('QuerySession falls back to the last assistant usage when the result has none', () => {
  const session = new QuerySession();
  session.handleMessage(assistantMessage([{ type: 'text', text: 'hi' }], { usage: { input_tokens: 4, output_tokens: 2 } }));
  session.handleMessage({ type: 'result', subtype: 'success', result: 'hi' });

  assert.deepEqual(session.resultPayload().usage, {
    input_tokens: 4,
    output_tokens: 2,
    cache_creation_input_tokens: 0,
    cache_read_input_tokens: 0,
  });
});

test('mapUsage returns null when there is nothing to report', () => {
  assert.equal(mapUsage(null), null);
  assert.equal(mapUsage(undefined), null);
  assert.equal(mapUsage({}), null);
  assert.deepEqual(mapUsage({ input_tokens: 1 }), {
    input_tokens: 1,
    output_tokens: 0,
    cache_creation_input_tokens: 0,
    cache_read_input_tokens: 0,
  });
});

// ---------------------------------------------------------------------------
// classifyError
// ---------------------------------------------------------------------------

test('classifyError maps the documented failure table', () => {
  const cases = [
    ['Invalid API key supplied', 'auth'],
    ['authentication_failed', 'auth'],
    ['401 Unauthorized', 'auth'],
    ['Please run /login to continue', 'auth'],
    ['not logged in', 'auth'],
    ['OAuth token expired', 'auth'],
    ['missing credentials file', 'auth'],
    ['Rate limit reached', 'rate_limit'],
    ['rate_limit', 'rate_limit'],
    ['429 Too Many Requests', 'rate_limit'],
    ['You have hit your usage limit', 'rate_limit'],
    ['monthly quota exceeded', 'rate_limit'],
    ['Overloaded', 'overloaded'],
    ['529 overloaded_error', 'overloaded'],
    ['503 Service Unavailable', 'overloaded'],
    ['request timed out', 'timeout'],
    ['ETIMEDOUT', 'timeout'],
    ['deadline exceeded', 'timeout'],
    ['invalid request: tool schema mismatch', 'format'],
    ['400 Bad Request', 'format'],
    ['malformed JSON', 'format'],
    ['something odd happened', 'unknown'],
    ['', 'unknown'],
  ];
  for (const [message, expected] of cases) {
    assert.equal(classifyError(message), expected, `message: ${message}`);
  }
});

test('classifyError accepts Error objects and scans the result subtype', () => {
  assert.equal(classifyError(new Error('boom: rate limit')), 'rate_limit');
  assert.equal(classifyError(undefined, 'error_during_execution'), 'unknown');
  assert.equal(classifyError({ message: 'oauth failure' }), 'auth');
  assert.equal(classifyError(null), 'unknown');
});

test('withAuthHint only augments auth failures, and only once', () => {
  assert.equal(withAuthHint('nope', 'auth'), `nope ${AUTH_HINT}`);
  assert.equal(withAuthHint('nope', 'timeout'), 'nope');
  assert.equal(withAuthHint(`nope ${AUTH_HINT}`, 'auth'), `nope ${AUTH_HINT}`);
  assert.equal(withAuthHint('', 'auth'), AUTH_HINT);
});// ---------------------------------------------------------------------------
// MCP server (built with fakes — no MCP SDK needed)
// ---------------------------------------------------------------------------

/** Minimal stand-in for the low-level MCP `Server`. */
class FakeServer {
  constructor(info, options) {
    this.info = info;
    this.options = options;
    this.handlers = new Map();
  }

  setRequestHandler(schema, handler) {
    this.handlers.set(schema, handler);
  }

  async call(schema) {
    return this.handlers.get(schema)({ method: schema, params: {} }, {});
  }
}

const LIST_TOOLS = { name: 'ListToolsRequestSchema' };
const CALL_TOOL = { name: 'CallToolRequestSchema' };

test('mapTools passes lele JSON Schema through unchanged', () => {
  const parameters = {
    type: 'object',
    properties: { city: { type: 'string', description: 'City name' } },
    required: ['city'],
    additionalProperties: false,
  };
  const definitions = mapTools([
    { type: 'function', function: { name: 'get_weather', description: 'Weather lookup', parameters } },
  ]);
  assert.deepEqual(definitions, [
    { name: 'get_weather', description: 'Weather lookup', inputSchema: parameters },
  ]);
  // unchanged means the very same shape, not a rewritten one
  assert.deepEqual(definitions[0].inputSchema, parameters);
});

test('mapTools defaults an empty schema and skips nameless tools', () => {
  const definitions = mapTools([
    { type: 'function', function: { name: 'no_args', parameters: {} } },
    { type: 'function', function: { name: '', parameters: { type: 'object' } } },
  ]);
  assert.deepEqual(definitions, [
    { name: 'no_args', description: '', inputSchema: { type: 'object', properties: {} } },
  ]);
});

test('createLeleMcpServer registers a tool list and a non-executing CallTool', async () => {
  const server = createLeleMcpServer({
    Server: FakeServer,
    ListToolsRequestSchema: LIST_TOOLS,
    CallToolRequestSchema: CALL_TOOL,
    tools: [{ type: 'function', function: { name: 'ping', description: 'Ping', parameters: { type: 'object' } } }],
  });

  assert.deepEqual(server.info, { name: 'lele', version: '1.0.0' });
  assert.deepEqual(server.options, { capabilities: { tools: {} } });

  const listed = await server.call(LIST_TOOLS);
  assert.deepEqual(listed.tools, [{ name: 'ping', description: 'Ping', inputSchema: { type: 'object' } }]);

  const called = await server.call(CALL_TOOL);
  assert.deepEqual(called, { content: [{ type: 'text', text: CAPTURED_TOOL_TEXT }] });
});

test('createLeleMcpServer rejects a missing Server class', () => {
  assert.throws(() => createLeleMcpServer({ ListToolsRequestSchema: LIST_TOOLS, CallToolRequestSchema: CALL_TOOL }), /Server class is required/);
});

// ---------------------------------------------------------------------------
// framing helpers
// ---------------------------------------------------------------------------

test('encodeLine produces one newline-terminated JSON line', () => {
  assert.equal(encodeLine({ type: 'ready', sdk_version: '1.0.0' }), '{"type":"ready","sdk_version":"1.0.0"}\n');
});

test('parseRequestLine parses objects, skips blanks and rejects junk', () => {
  assert.deepEqual(parseRequestLine('{"type":"init"}'), { type: 'init' });
  assert.equal(parseRequestLine('   '), null);
  assert.equal(parseRequestLine(''), null);
  assert.throws(() => parseRequestLine('not json'), SyntaxError);
  assert.throws(() => parseRequestLine('[]'), /must be a JSON object/);
  assert.throws(() => parseRequestLine('{"id":"x"}'), /missing a type/);
});

// ---------------------------------------------------------------------------
// SDK resolution (fake package on disk)
// ---------------------------------------------------------------------------

test('parseDataUrl accepts base64 data URLs only', () => {
  assert.deepEqual(parseDataUrl('data:image/jpeg;base64,QUJD'), { mediaType: 'image/jpeg', data: 'QUJD' });
  assert.equal(parseDataUrl('data:image/png,unencoded'), null);
  assert.equal(parseDataUrl('https://example.test/a.png'), null);
  assert.equal(parseDataUrl(undefined), null);
});

test('normalizeArguments tolerates JSON strings, objects and junk', () => {
  assert.deepEqual(normalizeArguments('{"a":1}'), { a: 1 });
  assert.deepEqual(normalizeArguments({ a: 1 }), { a: 1 });
  assert.deepEqual(normalizeArguments(''), {});
  assert.deepEqual(normalizeArguments(null), {});
  assert.deepEqual(normalizeArguments('{oops'), { _raw: '{oops' });
  assert.deepEqual(normalizeArguments('42'), { value: 42 });
});

test('resolveEntryFile follows exports, then module/main, then sdk.mjs', () => {
  const dir = '/tmp/pkg';
  assert.equal(
    resolveEntryFile(dir, { exports: { '.': { types: './sdk.d.ts', default: './sdk.mjs' } } }),
    path.resolve(dir, 'sdk.mjs'),
  );
  assert.equal(resolveEntryFile(dir, { main: 'index.js' }), path.resolve(dir, 'index.js'));
  assert.equal(resolveEntryFile(dir, {}), path.resolve(dir, 'sdk.mjs'));
  assert.equal(resolveEntryFile(dir, { exports: './dist/main.mjs' }), path.resolve(dir, 'dist/main.mjs'));
});

test('resolveSdk loads a package found through a search path', async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'lele-bridge-sdk-'));
  try {
    const pkgDir = path.join(root, '@anthropic-ai', 'claude-agent-sdk');
    mkdirSync(pkgDir, { recursive: true });
    writeFileSync(
      path.join(pkgDir, 'package.json'),
      JSON.stringify({ name: '@anthropic-ai/claude-agent-sdk', version: '9.9.9', type: 'module', exports: { '.': { default: './sdk.mjs' } } }),
    );
    writeFileSync(
      path.join(pkgDir, 'sdk.mjs'),
      'export function query() { return { query: true }; }\n',
    );

    const resolved = await resolveSdk([root]);
    assert.equal(resolved.version, '9.9.9');
    assert.equal(resolved.packageDir, pkgDir);
    assert.equal(typeof resolved.sdk.query, 'function');
    // no @modelcontextprotocol/sdk next to the fake package
    assert.equal(resolved.mcp, null);
    assert.equal(resolveMcpPaths(path.join(pkgDir, 'package.json')), null);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('resolveSdk reports the actionable install hint when nothing resolves', async () => {
  const empty = mkdtempSync(path.join(tmpdir(), 'lele-bridge-empty-'));
  try {
    // The bare specifier may legitimately resolve on a dev machine; only assert
    // the failure contract when it does not.
    let resolved = null;
    try {
      resolved = await resolveSdk([empty]);
    } catch (error) {
      assert.equal(error.message, INSTALL_HINT);
      assert.match(error.message, /npm install -g @anthropic-ai\/claude-agent-sdk/);
      assert.match(error.message, /CLAUDE_AGENT_SDK_PATH/);
      return;
    }
    assert.equal(typeof resolved.sdk.query, 'function');
  } finally {
    rmSync(empty, { recursive: true, force: true });
  }
});test('resolveSdk falls back to NODE_PATH entries', async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'lele-bridge-nodepath-'));
  const previous = process.env.NODE_PATH;
  try {
    const pkgDir = path.join(root, '@anthropic-ai', 'claude-agent-sdk');
    mkdirSync(pkgDir, { recursive: true });
    writeFileSync(
      path.join(pkgDir, 'package.json'),
      JSON.stringify({ name: '@anthropic-ai/claude-agent-sdk', version: '8.8.8', type: 'module', main: 'sdk.mjs' }),
    );
    writeFileSync(path.join(pkgDir, 'sdk.mjs'), 'export function query() { return {}; }\n');

    process.env.NODE_PATH = `${path.join(root, 'does-not-exist')}${path.delimiter}${root}`;
    const resolved = await resolveSdk([]);
    assert.equal(resolved.version, '8.8.8');
  } finally {
    if (previous === undefined) delete process.env.NODE_PATH;
    else process.env.NODE_PATH = previous;
    rmSync(root, { recursive: true, force: true });
  }
});