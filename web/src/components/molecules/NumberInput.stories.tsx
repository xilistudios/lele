import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { NumberInput } from './NumberInput'

const meta = {
  title: 'Molecules/NumberInput',
  component: NumberInput,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof NumberInput>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    id: 'num-1',
    value: 0,
    onChange: fn(),
  },
}

export const WithValue: Story = {
  args: {
    id: 'num-value',
    value: 42,
    onChange: fn(),
  },
}

export const WithMinMax: Story = {
  args: {
    id: 'num-minmax',
    value: 50,
    min: 0,
    max: 100,
    onChange: fn(),
  },
}

export const WithStep: Story = {
  args: {
    id: 'num-step',
    value: 25,
    step: 5,
    onChange: fn(),
  },
}

export const Disabled: Story = {
  args: {
    id: 'num-disabled',
    value: 10,
    disabled: true,
    onChange: fn(),
  },
}
