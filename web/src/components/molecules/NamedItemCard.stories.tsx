import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { NamedItemCard } from './NamedItemCard'

const meta = {
  title: 'Molecules/NamedItemCard',
  component: NamedItemCard,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof NamedItemCard>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    title: 'My Item',
    removeLabel: 'Remove item',
    onRemove: fn(),
    children: <p className="text-sm text-text-secondary">Card content</p>,
  },
}

export const Expanded: Story = {
  args: {
    title: 'My Item',
    removeLabel: 'Remove item',
    defaultCollapsed: false,
    onRemove: fn(),
    children: <p className="text-sm text-text-secondary">Card content</p>,
  },
}

export const WithComplexTitle: Story = {
  args: {
    title: (
      <span>
        Agent <strong className="font-semibold text-text-primary">coder</strong>
      </span>
    ),
    removeLabel: 'Remove agent',
    defaultCollapsed: false,
    onRemove: fn(),
    children: <p className="text-sm text-text-secondary">Card content</p>,
  },
}
