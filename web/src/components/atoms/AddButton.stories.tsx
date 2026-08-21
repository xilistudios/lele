import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { AddButton } from './AddButton'

const meta = {
  title: 'Atoms/AddButton',
  component: AddButton,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof AddButton>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    children: 'Add Item',
    onClick: fn(),
  },
}

export const Disabled: Story = {
  args: {
    children: 'Add Item',
    disabled: true,
    onClick: fn(),
  },
}
