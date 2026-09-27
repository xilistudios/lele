/**
 * Unit tests for `createSessionRefreshCoordinator` (no React, no timers).
 *
 * Covered invariants:
 *  - single flight: a burst of `run()` calls performs ONE execution;
 *  - trailing coalescing: N incoming calls schedule at most ONE trailing
 *    pass, and it uses the LAST task received;
 *  - cancellation: `cancel()` invalidates the in-flight pass (awaiters still
 *    receive the real resolution, never a rejection) and drops the pending
 *    coalescing;
 *  - failure: a rejected task rejects the shared promise, drops the
 *    coalescing and leaves the coordinator reusable (never stuck busy).
 */
import { describe, expect, mock, test } from 'bun:test'
import { createSessionRefreshCoordinator } from './sessionRefreshCoordinator'

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (error: unknown) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

/** Pushes the coordinator's internal promise chain to its next step. */
async function flush(times = 10): Promise<void> {
  for (let i = 0; i < times; i += 1) await Promise.resolve()
}

describe('createSessionRefreshCoordinator', () => {
  test('collapses a burst of concurrent run() calls with the same task into one execution', async () => {
    const coordinator = createSessionRefreshCoordinator<number>()
    const gate = deferred<number>()
    const task = mock(() => gate.promise)

    // Same task function, no await between the calls: exactly the shape of a
    // burst of refresh triggers coming from the same render/closure.
    const calls = Array.from({ length: 5 }, () => coordinator.run(task))

    // Threshold: 1 execution is REQUIRED here because all 5 calls are
    // synchronous, so the 5th arrives before the 1st can resolve and there is
    // no window in which a trailing pass could be scheduled from a *different*
    // task. (The coordinator's hard ceiling is 2: one leading + one trailing.)
    expect(task.mock.calls.length).toBe(1)
    expect(coordinator.isBusy()).toBe(true)
    // Every joiner receives the very same promise object.
    for (const promise of calls) {
      expect(promise).toBe(calls[0])
    }

    gate.resolve(42)
    await expect(Promise.all(calls)).resolves.toEqual([42, 42, 42, 42, 42])
    await flush()

    // No trailing pass: re-running the identical task is pure duplication.
    expect(task.mock.calls.length).toBe(1)
    expect(coordinator.isBusy()).toBe(false)
  })

  test('distinct tasks while in flight schedule exactly ONE trailing pass, with the LAST task', async () => {
    const coordinator = createSessionRefreshCoordinator<string>()
    const leadingGate = deferred<string>()
    const trailingGate = deferred<string>()
    const firstTask = mock(() => leadingGate.promise)
    const supersededTask = mock(() => Promise.resolve('superseded'))
    const lastTask = mock(() => trailingGate.promise)

    const first = coordinator.run(firstTask)
    // Three more calls while the pass is in flight: never one trailing pass
    // per call, and the trailing one must use the most recent task.
    const second = coordinator.run(supersededTask)
    const third = coordinator.run(lastTask)
    const fourth = coordinator.run(lastTask)

    expect(second).toBe(first)
    expect(third).toBe(first)
    expect(fourth).toBe(first)
    expect(firstTask.mock.calls.length).toBe(1)
    expect(supersededTask.mock.calls.length).toBe(0)
    expect(lastTask.mock.calls.length).toBe(0)

    leadingGate.resolve('leading')
    await expect(first).resolves.toBe('leading')
    await flush()

    expect(firstTask.mock.calls.length).toBe(1)
    // Only the LATEST task is kept.
    expect(supersededTask.mock.calls.length).toBe(0)
    // Exactly one trailing pass, not three.
    expect(lastTask.mock.calls.length).toBe(1)
    expect(coordinator.isBusy()).toBe(true)

    trailingGate.resolve('trailing')
    await flush()
    expect(coordinator.isBusy()).toBe(false)
  })

  test('cancel() invalidates the in-flight pass and drops the pending coalescing', async () => {
    const coordinator = createSessionRefreshCoordinator<number>()
    const gate = deferred<number>()
    const task = mock(() => gate.promise)
    const coalesced = mock(() => Promise.resolve(99))

    const inFlight = coordinator.run(task)
    const joined = coordinator.run(coalesced)
    expect(joined).toBe(inFlight)
    expect(coordinator.isBusy()).toBe(true)

    coordinator.cancel()
    // The pass is still physically in flight (its request is pending).
    expect(coordinator.isBusy()).toBe(true)

    gate.resolve(7)
    // Awaiters receive the ACTUAL resolution — a cancel never rejects them.
    await expect(inFlight).resolves.toBe(7)
    await flush()

    expect(coordinator.isBusy()).toBe(false)
    // The coalesced refresh was dropped: a reset/logout must not be followed
    // by a stale trailing pass.
    expect(coalesced.mock.calls.length).toBe(0)
  })

  test('after cancel(), the same task still schedules a fresh trailing pass', async () => {
    const coordinator = createSessionRefreshCoordinator<number>()
    const firstGate = deferred<number>()
    const secondGate = deferred<number>()
    let invocations = 0
    const task = () => {
      invocations += 1
      return invocations === 1 ? firstGate.promise : secondGate.promise
    }

    const first = coordinator.run(task)
    coordinator.cancel()
    const joined = coordinator.run(task)

    expect(joined).toBe(first)
    expect(invocations).toBe(1)

    firstGate.resolve(1)
    await expect(first).resolves.toBe(1)
    await flush()

    // The invalidated pass cannot be applied, so even an identical task must
    // run again to produce a usable result.
    expect(invocations).toBe(2)
    expect(coordinator.isBusy()).toBe(true)

    secondGate.resolve(2)
    await flush()
    expect(coordinator.isBusy()).toBe(false)
    expect(invocations).toBe(2)
  })

  test('a rejecting task rejects the shared promise, drops coalescing and stays reusable', async () => {
    const coordinator = createSessionRefreshCoordinator<number>()
    const gate = deferred<number>()
    const failure = new Error('meta endpoint exploded')
    let invocations = 0
    let coalescedInvocations = 0
    const failingTask = () => {
      invocations += 1
      return gate.promise
    }
    const coalesced = () => {
      coalescedInvocations += 1
      return Promise.resolve(1)
    }

    const inFlight = coordinator.run(failingTask)
    const joined = coordinator.run(coalesced)
    expect(joined).toBe(inFlight)

    gate.reject(failure)
    await expect(inFlight).rejects.toThrow('meta endpoint exploded')
    await flush()

    // Never stuck "in flight" after a rejection.
    expect(coordinator.isBusy()).toBe(false)
    expect(invocations).toBe(1)
    expect(coalescedInvocations).toBe(0)

    // A later call runs a brand new pass.
    let laterInvocations = 0
    const later = () => {
      laterInvocations += 1
      return Promise.resolve(123)
    }
    await expect(coordinator.run(later)).resolves.toBe(123)
    expect(laterInvocations).toBe(1)
    expect(coordinator.isBusy()).toBe(false)
  })

  test('isBusy() is false when idle and run() without any coalescing resolves normally', async () => {
    const coordinator = createSessionRefreshCoordinator<string | null>()
    expect(coordinator.isBusy()).toBe(false)
    await expect(coordinator.run(() => Promise.resolve(null))).resolves.toBeNull()
    expect(coordinator.isBusy()).toBe(false)
  })
})
