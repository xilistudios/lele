import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { GroupsIndicator } from './GroupsIndicator'

const meta = {
  title: 'Atoms/GroupsIndicator',
  component: GroupsIndicator,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
  args: {
    onClick: fn(),
  },
} satisfies Meta<typeof GroupsIndicator>

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

export const FiveCount: Story = {
  args: {
    count: 5,
  },
}

export const NinePlusCount: Story = {
  args: {
    count: 15,
  },
}
