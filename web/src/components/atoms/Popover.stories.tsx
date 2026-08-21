import type { Meta, StoryObj } from '@storybook/react'
import { Popover } from './Popover'

const meta = {
  title: 'Atoms/Popover',
  component: Popover,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof Popover>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    trigger: (
      <button
        type="button"
        className="rounded-md bg-accent-primary px-4 py-2 text-sm text-text-on-accent hover:bg-accent-hover"
      >
        Click me
      </button>
    ),
    children: (
      <div className="w-32 text-xs text-text-secondary">
        Popover content goes here. You can put anything in this popover.
      </div>
    ),
  },
}

export const WithTooltip: Story = {
  args: {
    trigger: (
      <button
        type="button"
        className="rounded-md bg-surface-secondary px-4 py-2 text-sm text-text-primary hover:bg-surface-hover"
      >
        Hover for tooltip
      </button>
    ),
    children: <div className="w-32 text-xs text-text-secondary">Popover content</div>,
    tooltip: 'Helpful tip',
  },
}

export const BlockLayout: Story = {
  args: {
    trigger: (
      <button
        type="button"
        className="w-full rounded-md bg-cta-primary px-4 py-2 text-sm text-text-on-accent hover:bg-cta-hover"
      >
        Full width trigger
      </button>
    ),
    children: <div className="w-32 text-xs text-text-secondary">Popover content</div>,
    block: true,
  },
  render: (args) => (
    <div className="w-64">
      <Popover {...args} />
    </div>
  ),
}
