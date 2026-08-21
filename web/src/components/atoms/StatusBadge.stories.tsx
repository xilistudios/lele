import type { Meta, StoryObj } from '@storybook/react'
import { StatusBadge } from './StatusBadge'

const meta = {
  title: 'Atoms/StatusBadge',
  component: StatusBadge,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof StatusBadge>

export default meta
type Story = StoryObj<typeof meta>

export const Executing: Story = {
  args: {
    status: 'executing',
  },
}

export const ErrorStatus: Story = {
  args: {
    status: 'error',
  },
}

export const Completed: Story = {
  args: {
    status: 'completed',
  },
  parameters: {
    docs: {
      description: {
        story: 'The completed status renders null (no badge shown) to reduce visual noise.',
      },
    },
  },
}
