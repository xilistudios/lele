import type { Meta, StoryObj } from '@storybook/react'
import { ErrorBanner } from './ErrorBanner'

const meta = {
  title: 'Atoms/ErrorBanner',
  component: ErrorBanner,
  tags: ['autodocs'],
  parameters: {
    layout: 'padded',
  },
} satisfies Meta<typeof ErrorBanner>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    message: 'Something went wrong',
  },
}

export const LongMessage: Story = {
  args: {
    message:
      'This is a very long error message that demonstrates how the banner handles wrapping and overflow in various scenarios and contexts',
  },
}
