export type RefreshResult<T> = { value: T }

export interface SessionRefreshCoordinator<T> {
  run: (task: () => Promise<T>) => Promise<T>
  cancel: () => void
  isBusy: () => boolean
}

interface InFlightPass<T> {
  generation: number
  task: () => Promise<T>
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (error: unknown) => void
}

type PassOutcome<T> = { ok: true; result: RefreshResult<T> } | { ok: false; error: unknown }

export function createSessionRefreshCoordinator<T>(): SessionRefreshCoordinator<T> {
  let generation = 0
  let inFlight: InFlightPass<T> | null = null
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
      pass.reject(outcome.error)
      return
    }

    if (trailing) {
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
      if (inFlight.generation !== generation || inFlight.task !== task) {
        trailingTask = task
      }
      return inFlight.promise
    }
    return startPass(task)
  }

  const cancel = (): void => {
    generation += 1
    trailingTask = null
  }

  return {
    run,
    cancel,
    isBusy: () => inFlight !== null,
  }
}
