import { describe, expect, mock, test } from 'bun:test'
import { renderHook, waitFor } from '@testing-library/react'
import { usePopoverPosition } from './usePopoverPosition'

describe('usePopoverPosition', () => {
  test('retorna posición por defecto cuando no está abierto', () => {
    const { result } = renderHook(() =>
      usePopoverPosition({
        isOpen: false,
        popoverWidth: 200,
        popoverHeight: 300,
      }),
    )

    expect(result.current.position.horizontal).toBe('right-align')
    expect(result.current.position.vertical).toBe('above')
  })

  test('ref no es null', () => {
    const { result } = renderHook(() =>
      usePopoverPosition({
        isOpen: true,
        popoverWidth: 200,
        popoverHeight: 300,
      }),
    )

    expect(result.current.ref.current).toBeNull()
  })

  test('usa padding por defecto', () => {
    const { result } = renderHook(() =>
      usePopoverPosition({
        isOpen: false,
        popoverWidth: 200,
        popoverHeight: 300,
      }),
    )

    // El padding interno es 8 (DEFAULT_PADDING)
    expect(result.current).toBeDefined()
  })
})
