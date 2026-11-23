import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { useState } from 'react'
import i18n from '../../test/i18n'
import { waitUntil } from '../../test/waitUntil'
import { SideDrawer } from './SideDrawer'

function DrawerHarness({ initiallyOpen = false }: { initiallyOpen?: boolean }) {
  const [isOpen, setIsOpen] = useState(initiallyOpen)
  return (
    <>
      <button type="button" onClick={() => setIsOpen(true)}>
        open
      </button>
      <button type="button" onClick={() => setIsOpen(false)}>
        close
      </button>
      <button type="button">behind the drawer</button>
      <SideDrawer
        isOpen={isOpen}
        onClose={() => setIsOpen(false)}
        ariaLabel="drawer"
        testId="drawer-panel"
      >
        <button type="button">drawer action</button>
        <button type="button">second drawer action</button>
        <p>drawer body</p>
      </SideDrawer>
    </>
  )
}

const panel = (view: ReturnType<typeof render>) => view.queryByRole('dialog', { name: 'drawer' })

afterEach(cleanup)

describe('SideDrawer', () => {
  test('renders nothing while closed', () => {
    const view = render(<DrawerHarness />)
    expect(panel(view)).toBeNull()
  })

  test('mounts the panel off-screen and then slides it into place', async () => {
    const view = render(<DrawerHarness />)
    fireEvent.click(view.getByRole('button', { name: 'open' }))

    expect(panel(view)?.className).toContain('translate-x-full')
    expect(panel(view)?.textContent).toContain('drawer body')

    await waitUntil(() => panel(view)?.className.includes('translate-x-0') ?? false)
  })

  test('keeps the panel mounted while the exit animation runs, then unmounts it', async () => {
    const view = render(<DrawerHarness initiallyOpen />)
    await waitUntil(() => panel(view)?.className.includes('translate-x-0') ?? false)

    fireEvent.click(view.getByRole('button', { name: 'close' }))

    expect(panel(view)?.className).toContain('translate-x-full')
    await waitUntil(() => panel(view) === null)
  })

  test('exposes the panel as a modal dialog', () => {
    const view = render(<DrawerHarness />)
    fireEvent.click(view.getByRole('button', { name: 'open' }))

    const drawer = panel(view)
    expect(drawer?.getAttribute('aria-modal')).toBe('true')
    expect(drawer?.hasAttribute('open')).toBe(true)
    expect(drawer?.getAttribute('aria-label')).toBe('drawer')
  })

  test('the backdrop is not a keyboard target', async () => {
    const view = render(<DrawerHarness />)
    fireEvent.click(view.getByRole('button', { name: 'open' }))
    await waitUntil(() => panel(view)?.className.includes('translate-x-0') ?? false)

    const closeLabel = i18n.t('common.close')
    expect(view.queryAllByRole('button', { name: closeLabel })).toHaveLength(0)
  })

  test('clicking the backdrop dismisses the drawer', async () => {
    const view = render(<DrawerHarness />)
    fireEvent.click(view.getByRole('button', { name: 'open' }))
    await waitUntil(() => panel(view)?.className.includes('translate-x-0') ?? false)

    const backdrop = view.container.querySelector('[aria-hidden="true"]') as HTMLElement
    fireEvent.click(backdrop)
    await waitUntil(() => panel(view) === null)
  })

  test('moves focus into the drawer and gives it back on dismiss', async () => {
    const view = render(<DrawerHarness />)
    const trigger = view.getByRole('button', { name: 'open' }) as HTMLButtonElement
    trigger.focus()
    fireEvent.click(trigger)

    await waitUntil(() => document.activeElement === panel(view))

    fireEvent.keyDown(document, { key: 'Escape' })
    await waitUntil(() => document.activeElement === trigger)
  })

  test('Tab wraps inside the open drawer instead of reaching the page behind', async () => {
    const view = render(<DrawerHarness />)
    fireEvent.click(view.getByRole('button', { name: 'open' }))
    await waitUntil(() => panel(view)?.className.includes('translate-x-0') ?? false)

    const first = view.getByRole('button', { name: 'drawer action' }) as HTMLButtonElement
    const last = view.getByRole('button', { name: 'second drawer action' }) as HTMLButtonElement

    last.focus()
    fireEvent.keyDown(document, { key: 'Tab' })
    expect(document.activeElement).toBe(first)

    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(last)
  })

  test('Tab is left alone once the drawer is closed', async () => {
    const view = render(<DrawerHarness />)
    fireEvent.click(view.getByRole('button', { name: 'open' }))
    await waitUntil(() => panel(view)?.className.includes('translate-x-0') ?? false)
    fireEvent.keyDown(document, { key: 'Escape' })
    await waitUntil(() => panel(view) === null)

    const behind = view.getByRole('button', { name: 'behind the drawer' }) as HTMLButtonElement
    behind.focus()

    const notPrevented = fireEvent.keyDown(document, { key: 'Tab' })
    expect(notPrevented).toBe(true)
    expect(document.activeElement).toBe(behind)
  })

  test('Escape closes the drawer only while it is open', () => {
    let closed = 0
    const handleClose = () => {
      closed += 1
    }
    const view = render(
      <SideDrawer isOpen onClose={handleClose} ariaLabel="drawer" testId="drawer-panel">
        <p>drawer body</p>
      </SideDrawer>,
    )

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(closed).toBe(1)

    view.rerender(
      <SideDrawer isOpen={false} onClose={handleClose} ariaLabel="drawer" testId="drawer-panel">
        <p>drawer body</p>
      </SideDrawer>,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(closed).toBe(1)
  })
})
