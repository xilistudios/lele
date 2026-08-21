import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { RemoveButton } from './RemoveButton'

const meta = {
  title: 'Atoms/RemoveButton',
  component: RemoveButton,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof RemoveButton>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    ariaLabel: 'Remove item',
    onClick: fn(),
  },
}
