import { describe, expect, test } from 'bun:test'
import { defaultReconnectStrategy } from './reconnect'

describe('defaultReconnectStrategy', () => {
  test('usa valores por defecto', () => {
    const strategy = defaultReconnectStrategy()
    expect(strategy.initialDelay).toBe(500)
    expect(strategy.maxDelay).toBe(5000)
    expect(strategy.factor).toBe(2)
    expect(strategy.maxRetries).toBe(20)
  })

  test('nextDelay duplica hasta maxDelay', () => {
    const strategy = defaultReconnectStrategy()
    expect(strategy.nextDelay(500)).toBe(1000)
    expect(strategy.nextDelay(1000)).toBe(2000)
    expect(strategy.nextDelay(2000)).toBe(4000)
    expect(strategy.nextDelay(3000)).toBe(5000) // capped at maxDelay
    expect(strategy.nextDelay(5000)).toBe(5000)
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

  test('nextDelay usa factor personalizado', () => {
    const strategy = defaultReconnectStrategy({ factor: 3, maxDelay: 10000 })
    expect(strategy.nextDelay(500)).toBe(1500)
    expect(strategy.nextDelay(1500)).toBe(4500)
  })
})
