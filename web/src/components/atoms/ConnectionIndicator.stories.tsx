import type { Meta, StoryObj } from '@storybook/react'
import { ConnectionIndicator } from './ConnectionIndicator'

const meta = {
  title: 'Atoms/ConnectionIndicator',
  component: ConnectionIndicator,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof ConnectionIndicator>

export default meta
type Story = StoryObj<typeof meta>

export const Connected: Story = {
  args: {
    status: 'connected',
    apiUrl: 'http://localhost:18793',
  },
}

export const Connecting: Story = {
  args: {
    status: 'connecting',
    apiUrl: 'http://localhost:18793',
  },
}

export const Disconnected: Story = {
  args: {
    status: 'disconnected',
    apiUrl: 'http://localhost:18793',
  },
}
