import { describe, expect, test } from 'bun:test'
import { computeJitteredDelay, defaultReconnectStrategy } from './reconnect'

describe('defaultReconnectStrategy', () => {
  test('usa valores por defecto', () => {
    const strategy = defaultReconnectStrategy()
    expect(strategy.initialDelay).toBe(500)
    expect(strategy.maxDelay).toBe(30000)
    expect(strategy.factor).toBe(2)
    expect(strategy.maxRetries).toBe(Number.POSITIVE_INFINITY)
  })

  test('nextDelay aplica jitter dentro del rango esperado', () => {
    const strategy = defaultReconnectStrategy()
    for (let i = 0; i < 50; i++) {
      const result = strategy.nextDelay(500)
      expect(result).toBeGreaterThanOrEqual(700)
      expect(result).toBeLessThanOrEqual(1300)
    }
  })

  test('nextDelay respeta maxDelay como cap', () => {
    const strategy = defaultReconnectStrategy()
    for (let i = 0; i < 50; i++) {
      const result = strategy.nextDelay(50000)
      expect(result).toBeLessThanOrEqual(39000)
      expect(result).toBeGreaterThanOrEqual(21000)
    }
  })

  test('permite sobrescribir valores', () => {
    const strategy = defaultReconnectStrategy({
      initialDelay: 1000,
      maxDelay: 10000,
      factor: 3,
      maxRetries: 10,
    })
    expect(strategy.initialDelay).toBe(1000)
    expect(strategy.maxDelay).toBe(10000)
    expect(strategy.factor).toBe(3)
    expect(strategy.maxRetries).toBe(10)
  })

  test('nextDelay usa factor personalizado con jitter', () => {
    const strategy = defaultReconnectStrategy({ factor: 3, maxDelay: 10000 })
    for (let i = 0; i < 50; i++) {
      const result = strategy.nextDelay(500)
      expect(result).toBeGreaterThanOrEqual(1050)
      expect(result).toBeLessThanOrEqual(1950)
    }
  })
})

describe('computeJitteredDelay', () => {
  test('aplica jitter de ±30%', () => {
    for (let i = 0; i < 100; i++) {
      const result = computeJitteredDelay(1000, 2, 30000)
      expect(result).toBeGreaterThanOrEqual(1400)
      expect(result).toBeLessThanOrEqual(2600)
    }
  })

  test('respeta maxDelay antes del jitter', () => {
    for (let i = 0; i < 100; i++) {
      const result = computeJitteredDelay(20000, 2, 30000)
      expect(result).toBeGreaterThanOrEqual(21000)
      expect(result).toBeLessThanOrEqual(39000)
    }
  })
})
