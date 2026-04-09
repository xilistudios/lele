export type ReconnectStrategy = {
  initialDelay: number
  maxDelay: number
  factor: number
  maxRetries: number
  nextDelay: (current: number) => number
}

export const defaultReconnectStrategy = (
  overrides?: Partial<ReconnectStrategy>,
): ReconnectStrategy => {
  const factor = overrides?.factor ?? 2
  const initialDelay = overrides?.initialDelay ?? 500
  const maxDelay = overrides?.maxDelay ?? 5000
  const maxRetries = overrides?.maxRetries ?? 20
  const nextDelay =
    overrides?.nextDelay ?? ((current: number) => Math.min(current * factor, maxDelay))

  return { initialDelay, maxDelay, factor, maxRetries, nextDelay }
}
