import type { Meta, StoryObj } from '@storybook/react'
import { Card } from './Card'

const meta = {
  title: 'Atoms/Card',
  component: Card,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof Card>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    children: 'Card content',
  },
}

export const WithContent: Story = {
  args: { children: 'Card' },
  render: () => (
    <Card className="w-80">
      <h3 className="mb-2 text-sm font-semibold text-text-primary">Card Title</h3>
      <p className="mb-4 text-xs text-text-secondary">
        This is a card with some content inside, demonstrating how the Card component works with
        richer children.
      </p>
      <button
        type="button"
        className="rounded bg-cta-primary px-3 py-1.5 text-xs text-text-on-accent hover:bg-cta-hover"
      >
        Action
      </button>
    </Card>
  ),
}
