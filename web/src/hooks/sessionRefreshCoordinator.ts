/**
 * Single-flight coordinator for the session-list refresh.
 *
 * Problem this solves: `refreshSessions()` is triggered from 4 independent
 * paths (bootstrap, WebSocket reconnect, debounced session events, and
 * clearSession). Every call walked the WHOLE pagination loop, so a burst of
 * triggers produced several identical passes over
 * `GET /api/v1/chat/sessions/meta` (visible as repeated
 * `?include_system=true&offset=200&limit=200` requests).
 *
 * This module is intentionally free of React (and of timers) so it can be
 * unit-tested with plain deferred promises. It guarantees:
 *
 * 1. **Single flight.** At most ONE pass runs at a time. Calls arriving while
 *    a pass is in flight do not start a second one: they join the pass in
 *    progress (they receive the very same promise, so every awaiter resolves
 *    with the same value).
 * 2. **Trailing coalescing.** The last task received while a pass was in
 *    flight is remembered and, once the in-flight pass settles successfully,
 *    executed as EXACTLY ONE trailing pass (never one per call: 5 incoming
 *    calls → at most 1 trailing pass). The trailing pass uses the LATEST
 *    task, because `token` / `clientId` / `api` may have changed since the
 *    leading pass started. An *identical* task (same function reference, i.e.
 *    the same refresh closure re-triggered) is skipped: re-running it would
 *    be pure duplication, and skipping it is what collapses a synchronous
 *    burst of the same trigger into a SINGLE paginated pass.
 * 3. **Cancellation.** `cancel()` bumps a generation, drops any coalesced
 *    trailing pass and marks the in-flight pass as invalidated. Awaiters of
 *    the in-flight pass still resolve with its actual resolution (they are
 *    never rejected because of a cancel); it is the caller's job to discard
 *    the value of an invalidated pass (see the generation guard in
 *    `useChatSessions`).
 * 4. **No stuck state.** If a task rejects, the shared promise rejects with
 *    that same error, the pending coalescing is dropped and the coordinator
 *    is immediately reusable (`isBusy()` back to false).
 *
 * Accepted trade-offs (deliberate, not oversights):
 *
 * 1. **A hung pass blocks the sidebar refresh.** While one pass is in flight
 *    no other can start, so a request that never settles freezes session-list
 *    refreshes until its requests fail by timeout (~30 s, times the retries of
 *    each page). Accepted in exchange for eliminating the burst storm, and
 *    bounded now by the pagination cap in `fetchAllSessions` (it cannot walk
 *    unbounded pages while it waits).
 * 2. **Tab-focus revalidation bypasses the throttle.** `useAppLogic` issues a
 *    full pass per tab focus without consulting the throttle window. The cost
 *    is bounded (one pass per user-initiated focus, and the coordinator
 *    collapses it if a pass is already running) and user-controlled, exactly
 *    like `refetchOnWindowFocus` on the react-query endpoints.
 */

/** Outcome holder for a pass (kept tiny on purpose: internal bookkeeping). */
export type RefreshResult<T> = { value: T }

export interface SessionRefreshCoordinator<T> {
  /**
   * Runs `task` exclusively. If a pass is already in flight, no second pass
   * is started: `coalesced` is recorded and the promise of the pass in
   * progress is returned. Once that pass settles (successfully), at most one
   * trailing pass runs, using the last task received.
   */
  run: (task: () => Promise<T>) => Promise<T>
  /**
   * Invalidates the result of the pass in flight: its result must not be
   * applied (awaiters still receive the actual resolution, never a reject)
   * and any coalesced trailing pass is dropped. Returns void.
   */
  cancel: () => void
  /** true while a pass (leading or trailing) is in flight. */
  isBusy: () => boolean
}

interface InFlightPass<T> {
  /** Generation at the moment the pass started (compared against the current
   *  one to know whether `cancel()` invalidated it). */
  generation: number
  /** Task currently executing (used to skip a redundant trailing re-run). */
  task: () => Promise<T>
  /** Shared promise handed to every awaiter of this pass. */
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (error: unknown) => void
}

type PassOutcome<T> = { ok: true; result: RefreshResult<T> } | { ok: false; error: unknown }

export function createSessionRefreshCoordinator<T>(): SessionRefreshCoordinator<T> {
  /** Bumped by `cancel()`; a pass whose generation is stale is invalidated. */
  let generation = 0
  let inFlight: InFlightPass<T> | null = null
  /** Latest task received while a pass was in flight, as long as it DIFFERS
   *  from the task in flight (trailing source). An identical re-trigger never
   *  replaces it: see `run()`. */
  let trailingTask: (() => Promise<T>) | null = null

  const settle = (pass: InFlightPass<T>, outcome: PassOutcome<T>): void => {
    if (inFlight === pass) {
      inFlight = null
    }
    const trailing = trailingTask
    trailingTask = null

    if (outcome.ok) {
      pass.resolve(outcome.result.value)
    } else {
      // A failed pass must not be followed by a trailing one: the coalesced
      // task is dropped (per spec) and awaiters of the joined calls reject
      // with the same error, since they share `pass.promise`.
      pass.reject(outcome.error)
      return
    }

    // Exactly one trailing pass, with the latest task received that DIFFERED
    // from the one in flight (an identical re-trigger is dropped by `run()`,
    // so it never replaces the retained trailing task). `settle` is the only
    // place that starts a trailing pass, so at most one can exist. Whether a
    // trailing pass is needed at all is decided by `run()` (it is skipped when
    // the very same task is already executing), so no task identity check is
    // repeated here.
    if (trailing) {
      // Nobody awaits the trailing pass directly (callers joined the leading
      // one), so its rejection must be handled here or it would surface as an
      // unhandled rejection. Swallowing it silently would hide a failed
      // refresh, so it is logged instead: the next trigger retries anyway.
      startPass(trailing).catch((error: unknown) => {
        console.warn('[sessionRefresh] trailing refresh failed:', error)
      })
    }
  }

  const startPass = (task: () => Promise<T>): Promise<T> => {
    let resolve!: (value: T) => void
    let reject!: (error: unknown) => void
    const promise = new Promise<T>((res, rej) => {
      resolve = res
      reject = rej
    })
    const pass: InFlightPass<T> = { generation, task, promise, resolve, reject }
    inFlight = pass
    void (async () => {
      try {
        const value = await task()
        settle(pass, { ok: true, result: { value } })
      } catch (error) {
        settle(pass, { ok: false, error })
      }
    })()
    return promise
  }

  const run = (task: () => Promise<T>): Promise<T> => {
    if (inFlight) {
      // Coalesce onto the pass in progress: same promise, same value.
      if (inFlight.generation !== generation || inFlight.task !== task) {
        // Either the in-flight pass was invalidated by `cancel()` (so its
        // value is unusable and a fresh pass is required), or the caller
        // brings a different task — remember only the LATEST one, so N
        // incoming calls schedule at most 1 trailing pass.
        trailingTask = task
      }
      return inFlight.promise
    }
    return startPass(task)
  }

  const cancel = (): void => {
    generation += 1
    // A reset/logout must not be followed by a stale refresh, so any pending
    // coalescing is dropped here.
    trailingTask = null
  }

  return {
    run,
    cancel,
    isBusy: () => inFlight !== null,
  }
}
