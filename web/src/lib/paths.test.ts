import { afterEach, describe, expect, test } from 'bun:test'
import { expandHomeDisplay, getHomeDir, setHomeDir } from './paths'

afterEach(() => setHomeDir(''))

describe('expandHomeDisplay (display only)', () => {
  test('without a registered home, ~ stays literally as stored', () => {
    expect(expandHomeDisplay('~/.lele/workspace-coder')).toBe('~/.lele/workspace-coder')
    expect(expandHomeDisplay('~')).toBe('~')
  })

  test('with a registered home, ~/… resolves and ~ alone becomes home', () => {
    setHomeDir('/home/alfredo')
    expect(expandHomeDisplay('~/.lele/workspace-coder')).toBe('/home/alfredo/.lele/workspace-coder')
    expect(expandHomeDisplay('~')).toBe('/home/alfredo')
    expect(getHomeDir()).toBe('/home/alfredo')
  })

  test('non-home paths are untouched', () => {
    setHomeDir('/home/alfredo')
    expect(expandHomeDisplay('/srv/lele')).toBe('/srv/lele')
    expect(expandHomeDisplay('relative/dir')).toBe('relative/dir')
    expect(expandHomeDisplay('~user/x')).toBe('~user/x') // backend does not support it either
    expect(expandHomeDisplay('')).toBe('')
  })

  test('never mutates the input (config keeps the raw value)', () => {
    setHomeDir('/home/alfredo')
    const stored = '~/x'
    expandHomeDisplay(stored)
    expect(stored).toBe('~/x')
  })
})
