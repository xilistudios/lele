import type { Meta, StoryObj } from '@storybook/react'
import { Logo } from './Logo'

const meta = {
  title: 'Atoms/Logo',
  component: Logo,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
    backgrounds: {
      default: 'dark',
    },
  },
} satisfies Meta<typeof Logo>

export default meta
type Story = StoryObj<typeof meta>

export const Full: Story = {
  args: {
    collapsed: false,
  },
}

export const Collapsed: Story = {
  args: {
    collapsed: true,
  },
}
