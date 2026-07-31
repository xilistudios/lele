export type ReconnectStrategy = {
  initialDelay: number
  maxDelay: number
  factor: number
  maxRetries: number
  nextDelay: (current: number) => number
}

export const computeJitteredDelay = (
  current: number,
  factor: number,
  maxDelay: number,
): number => {
  const delay = Math.min(current * factor, maxDelay)
  return delay * (0.7 + Math.random() * 0.6)
}

export const defaultReconnectStrategy = (
  overrides?: Partial<ReconnectStrategy>,
): ReconnectStrategy => {
  const factor = overrides?.factor ?? 2
  const initialDelay = overrides?.initialDelay ?? 500
  const maxDelay = overrides?.maxDelay ?? 30000
  const maxRetries = overrides?.maxRetries ?? Infinity
  const nextDelay =
    overrides?.nextDelay ?? ((current: number) => computeJitteredDelay(current, factor, maxDelay))

  return { initialDelay, maxDelay, factor, maxRetries, nextDelay }
}
