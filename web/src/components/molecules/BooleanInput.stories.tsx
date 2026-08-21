import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { BooleanInput } from './BooleanInput'

const meta = {
  title: 'Molecules/BooleanInput',
  component: BooleanInput,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof BooleanInput>

export default meta
type Story = StoryObj<typeof meta>

export const Checked: Story = {
  args: {
    id: 'bool-checked',
    value: true,
    onChange: fn(),
  },
}

export const Unchecked: Story = {
  args: {
    id: 'bool-unchecked',
    value: false,
    onChange: fn(),
  },
}

export const Disabled: Story = {
  args: {
    id: 'bool-disabled',
    value: true,
    disabled: true,
    onChange: fn(),
  },
}

export const WithCustomLabel: Story = {
  args: {
    id: 'bool-label',
    value: false,
    label: 'Enable notifications',
    onChange: fn(),
  },
}
