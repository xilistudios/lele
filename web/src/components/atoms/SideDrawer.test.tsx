import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { useState } from 'react'
import '../../test/i18n'
import { SideDrawer } from './SideDrawer'

function DrawerHarness({ initiallyOpen = false }: { initiallyOpen?: boolean }) {
  const [isOpen, setIsOpen] = useState(initiallyOpen)
  return (
    <>
      <button type="button" data-testid="open" onClick={() => setIsOpen(true)}>
        open
      </button>
      <button type="button" data-testid="close" onClick={() => setIsOpen(false)}>
        close
      </button>
      <SideDrawer
        isOpen={isOpen}
        onClose={() => setIsOpen(false)}
        ariaLabel="drawer"
        testId="drawer-panel"
      >
        <p>drawer body</p>
      </SideDrawer>
    </>
  )
}

const panel = (view: ReturnType<typeof render>) => view.queryByTestId('drawer-panel')

afterEach(cleanup)

describe('SideDrawer', () => {
  test('renders nothing while closed', () => {
    const view = render(<DrawerHarness />)
    expect(panel(view)).toBeNull()
  })

  test('mounts the panel off-screen and then slides it into place', async () => {
    const view = render(<DrawerHarness />)
    fireEvent.click(view.getByTestId('open'))

    expect(panel(view)?.className).toContain('translate-x-full')
    expect(panel(view)?.textContent).toContain('drawer body')

    await waitFor(() => expect(panel(view)?.className).toContain('translate-x-0'))
  })

  test('keeps the panel mounted while the exit animation runs, then unmounts it', async () => {
    const view = render(<DrawerHarness initiallyOpen />)
    await waitFor(() => expect(panel(view)?.className).toContain('translate-x-0'))

    fireEvent.click(view.getByTestId('close'))

    expect(panel(view)?.className).toContain('translate-x-full')
    await waitFor(() => expect(panel(view)).toBeNull())
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
