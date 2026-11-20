// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

/**
 * Claude Agent SDK bridge for lele.
 *
 * lele (Go) spawns this script once and speaks JSON lines with it over stdio:
 *
 *   Go -> bridge: init | chat | cancel
 *   bridge -> Go: ready | fatal | chunk | reasoning | result | error | log
 *
 * stdout carries protocol JSON only; every other diagnostic goes to stderr (Go
 * logs it at debug level). The process exits cleanly on stdin EOF.
 *
 * The SDK is resolved lazily on `init` and its `query()` is used as a SINGLE
 * model step: built-in Claude Code tools are disabled, lele's tools are exposed
 * through an in-process MCP server, and tool calls are captured and interrupted
 * so lele can execute them itself. All SDK interaction lives behind
 * bridge_lib.mjs; this file owns stdio, timers and the AbortControllers.
 */

import { createInterface } from 'node:readline';
import {
  DENY_MESSAGE,
  INSTALL_HINT,
  QuerySession,
  buildPrompt,
  buildQueryOptions,
  buildTranscript,
  classifyError,
  createLeleMcpServer,
  encodeLine,
  loadMcpModules,
  parseRequestLine,
  resolveSdk,
  withAuthHint,
} from './bridge_lib.mjs';

/** Cap on how long we wait for `q.interrupt()` before draining anyway. */
const INTERRUPT_TIMEOUT_MS = 5000;

/** Resolved SDK state, set by `init`. */
let sdkState = null;
/** Set once init failed: later chats are rejected without retrying. */
let fatalMessage = '';
/**
 * In-flight `init`. Go waits for `ready` before sending a chat, but a chat that
 * arrives while resolution is still running must wait for it instead of
 * failing (resolution can take a few hundred ms on a cold NODE_PATH lookup).
 */
let initPromise = null;

/** Live chats, keyed by request id. */
const activeRequests = new Map();
/** Cached MCP module imports (`{Server, ...}`), loaded on first tool-bearing chat. */
let mcpModulesPromise = null;

// ---------------------------------------------------------------------------
// stdio plumbing
// ---------------------------------------------------------------------------

/** Write one protocol event to stdout. */
function emit(event) {
  try {
    process.stdout.write(encodeLine(event));
  } catch (error) {
    // stdout is gone: nothing useful left to do but record why on stderr.
    process.stderr.write(`lele-claude-bridge: stdout write failed: ${error?.message ?? error}\n`);
  }
}

/** Emit a `log` event (Go forwards these to its logger at debug level). */
function emitLog(level, message) {
  const text = String(message ?? '').trim();
  if (text === '') return;
  emit({ type: 'log', level, message: text });
}

/** True when the error is an abort/cancel (not a real failure). */
function isAbortError(error) {
  if (!error) return false;
  return error.name === 'AbortError' || /aborted|abort error/i.test(String(error.message ?? ''));
}

/** Resolve `promise`, or reject with a timeout after `ms` (best effort). */
function withTimeout(promise, ms, label) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms);
    timer.unref?.();
    Promise.resolve(promise).then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  });
}

// ---------------------------------------------------------------------------
// request handlers
// ---------------------------------------------------------------------------

/** `init` handler: resolve the SDK once and answer `ready` (or `fatal`). */
async function runInit(request) {
  const searchPaths = Array.isArray(request.sdk_search_paths) ? request.sdk_search_paths : [];
  const claudePath = typeof request.claude_path === 'string' ? request.claude_path : '';

  try {
    const resolved = await resolveSdk(searchPaths);
    sdkState = { ...resolved, claudePath };
    emit({ type: 'ready', sdk_version: resolved.version, claude_path: claudePath });
  } catch (error) {
    fatalMessage = INSTALL_HINT;
    process.stderr.write(`lele-claude-bridge: SDK resolution failed: ${error?.message ?? error}\n`);
    // Exit only after the fatal line is flushed, otherwise Go can see EOF first.
    process.stdout.write(encodeLine({ type: 'fatal', message: fatalMessage }), () => {
      process.exit(1);
    });
  }
}

/** Start (or reuse) the single SDK resolution. */
function handleInit(request) {
  if (sdkState) {
    emit({ type: 'ready', sdk_version: sdkState.version, claude_path: sdkState.claudePath });
    return initPromise;
  }
  if (!initPromise) initPromise = runInit(request);
  return initPromise;
}

/** Lazily import + build the MCP server entry for a chat's tools. */
async function buildMcpServerEntry(tools) {
  if (tools.length === 0) return null;
  if (!sdkState?.mcp) {
    throw new Error(
      'the installed @anthropic-ai/claude-agent-sdk has no @modelcontextprotocol/sdk dependency, so lele tools cannot be exposed',
    );
  }
  if (!mcpModulesPromise) mcpModulesPromise = loadMcpModules(sdkState.mcp);
  const modules = await mcpModulesPromise;
  return {
    type: 'sdk',
    name: 'lele',
    instance: createLeleMcpServer({ ...modules, tools }),
  };
}

/**
 * Close a query, swallowing the errors a closed transport can throw.
 * `Query.close()` is synchronous in every SDK version we support.
 */
function closeQuery(query, id) {
  try {
    query.close?.();
  } catch (error) {
    emitLog('debug', `close failed for ${id}: ${error?.message ?? error}`);
  }
}

/**
 * Build the `result` protocol event, omitting the optional fields when empty
 * (the spec asks for `tool_calls`, `reasoning` and `usage` to be absent then).
 */
function resultEvent(id, payload) {
  const event = {
    type: 'result',
    id,
    content: payload.content,
    finish_reason: payload.finish_reason,
  };
  if (payload.tool_calls.length > 0) event.tool_calls = payload.tool_calls;
  if (payload.reasoning) event.reasoning = payload.reasoning;
  if (payload.usage) event.usage = payload.usage;
  return event;
}

/** Emit the terminal event (result or error) for a finished turn. */
function emitOutcome(request, session) {
  const id = request.id;
  if (!session.resultSeen) {
    emitLog('debug', `query for ${id} ended without a result message`);
  }

  // Captured tool calls win: lele executes them and calls back with the
  // appended history, so a turn that produced calls always reports tool_calls.
  if (session.toolCalls.length > 0) {
    emit(resultEvent(id, session.resultPayload()));
    return;
  }

  if (session.isError) {
    const payload = session.errorPayload();
    emit({ type: 'error', id, message: payload.message, class: payload.class });
    return;
  }

  emit(resultEvent(id, session.resultPayload()));
}

/** Run one `chat` request to completion. Never throws. */
async function handleChat(request) {
  const id = typeof request.id === 'string' ? request.id : '';
  if (id === '') {
    emitLog('warn', 'ignoring chat request without an id');
    return;
  }

  // A chat can only run once the SDK is resolved; wait for an in-flight init.
  if (initPromise) {
    try {
      await initPromise;
    } catch (error) {
      emitLog('error', `init failed: ${error?.message ?? error}`);
    }
  }
  if (!sdkState) {
    emit({
      type: 'error',
      id,
      message: fatalMessage || 'Claude Code bridge is not initialized',
      class: classifyError(fatalMessage || 'not initialized'),
    });
    return;
  }

  const abortController = new AbortController();
  const entry = { abortController };
  activeRequests.set(id, entry);

  try {
    const tools = Array.isArray(request.tools) ? request.tools : [];
    const transcript = buildTranscript(request.messages);
    const mcpServer = await buildMcpServerEntry(tools);
    const session = new QuerySession({ stream: Boolean(request.stream) });

    const options = buildQueryOptions({
      systemPrompt: typeof request.system === 'string' ? request.system : '',
      tools,
      mcpServer,
      model: typeof request.model === 'string' ? request.model : '',
      stream: Boolean(request.stream),
      claudePath: sdkState.claudePath,
      abortController,
      canUseTool: async (name, input, callbackOptions) => {
        // Capture is best effort: the assistant message is authoritative and
        // `recordToolUse` deduplicates by id, so either order is safe.
        session.recordToolUse(name, input, callbackOptions?.toolUseID);
        return { behavior: 'deny', message: DENY_MESSAGE };
      },
      stderr: (data) => emitLog('debug', String(data ?? '')),
      env: process.env,
    });

    const query = sdkState.sdk.query({ prompt: buildPrompt(transcript), options });
    let drainTimer = null;

    try {
      for await (const message of query) {
        if (abortController.signal.aborted) break;
        const effects = session.handleMessage(message);

        for (const text of effects.chunks) emit({ type: 'chunk', id, text });
        for (const text of effects.reasoning) emit({ type: 'reasoning', id, text });

        if (effects.interrupt) {
          // Start the grace window first: `interrupt()` is itself a round trip
          // to the CLI and must not eat into it.
          const remaining = Math.max(0, session.drainDeadline - Date.now());
          drainTimer = setTimeout(() => closeQuery(query, id), remaining);
          drainTimer.unref?.();
          try {
            await withTimeout(query.interrupt(), INTERRUPT_TIMEOUT_MS, 'interrupt');
          } catch (error) {
            emitLog('warn', `interrupt failed for ${id}: ${error?.message ?? error}`);
          }
        }

        if (effects.done) break;
      }
    } finally {
      if (drainTimer) clearTimeout(drainTimer);
      closeQuery(query, id);
    }

    if (abortController.signal.aborted) return; // cancelled: Go already gave up
    emitOutcome(request, session);
  } catch (error) {
    if (abortController.signal.aborted || isAbortError(error)) return;
    const errorClass = classifyError(error);
    emitLog('error', `chat ${id} failed: ${error?.message ?? error}`);
    emit({
      type: 'error',
      id,
      message: withAuthHint(error?.message ?? String(error), errorClass),
      class: errorClass,
    });
  } finally {
    activeRequests.delete(id);
  }
}

/** `cancel`: abort one in-flight chat. Unknown ids are ignored. */
function handleCancel(request) {
  const id = typeof request.id === 'string' ? request.id : '';
  if (id === '') return;
  const entry = activeRequests.get(id);
  if (!entry) return;
  try {
    entry.abortController.abort();
  } catch (error) {
    emitLog('debug', `abort failed for ${id}: ${error?.message ?? error}`);
  }
}

// ---------------------------------------------------------------------------
// main loop
// ---------------------------------------------------------------------------

/** Abort every in-flight chat (used on stdin EOF and on signals). */
function abortAll() {
  for (const entry of activeRequests.values()) {
    try {
      entry.abortController.abort();
    } catch {
      // shutting down anyway
    }
  }
  activeRequests.clear();
}

function shutdown() {
  abortAll();
  process.exitCode = 0;
  // The SDK's child process can outlive the abort by a moment; an unref'd
  // timer bounds the wait without keeping the loop alive on its own.
  const timer = setTimeout(() => process.exit(0), 500);
  timer.unref?.();
}

function handleLine(line) {
  let request;
  try {
    request = parseRequestLine(line);
  } catch (error) {
    emitLog('warn', `ignoring malformed request line: ${error?.message ?? error}`);
    return;
  }
  if (!request) return;

  switch (request.type) {
    case 'init':
      void handleInit(request);
      break;
    case 'chat':
      // Not awaited: one process serves concurrent chats, and a failure must
      // never take down the others.
      void handleChat(request);
      break;
    case 'cancel':
      handleCancel(request);
      break;
    default:
      emitLog('warn', `ignoring unknown request type: ${request.type}`);
      break;
  }
}

function main() {
  // A single request's crash must never kill the bridge.
  process.on('uncaughtException', (error) => {
    emitLog('error', `uncaught exception: ${error?.stack ?? error}`);
  });
  process.on('unhandledRejection', (reason) => {
    emitLog('error', `unhandled rejection: ${reason?.stack ?? reason}`);
  });
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    process.on(signal, () => {
      process.stderr.write(`lele-claude-bridge: received ${signal}, exiting\n`);
      shutdown();
      process.exit(0);
    });
  }

  const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
  input.on('line', handleLine);
  input.on('close', shutdown);
  input.on('error', (error) => {
    process.stderr.write(`lele-claude-bridge: stdin error: ${error?.message ?? error}\n`);
    shutdown();
  });
}

main();