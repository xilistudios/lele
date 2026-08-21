import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { SubagentsIndicator } from './SubagentsIndicator'

const meta = {
  title: 'Atoms/SubagentsIndicator',
  component: SubagentsIndicator,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
  args: {
    onClick: fn(),
  },
} satisfies Meta<typeof SubagentsIndicator>

export default meta
type Story = StoryObj<typeof meta>

export const ZeroCount: Story = {
  args: {
    count: 0,
  },
}

export const OneCount: Story = {
  args: {
    count: 1,
  },
}

export const ThreeCount: Story = {
  args: {
    count: 3,
  },
}

export const NinePlusCount: Story = {
  args: {
    count: 12,
  },
}
