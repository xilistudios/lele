import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { TextInput } from './TextInput'

const meta = {
  title: 'Molecules/TextInput',
  component: TextInput,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof TextInput>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    id: 'text-1',
    value: '',
    placeholder: 'Enter text...',
    onChange: fn(),
  },
}

export const WithValue: Story = {
  args: {
    id: 'text-value',
    value: 'Hello World',
    onChange: fn(),
  },
}

export const Disabled: Story = {
  args: {
    id: 'text-disabled',
    value: 'Cant type here',
    disabled: true,
    onChange: fn(),
  },
}

export const PasswordType: Story = {
  args: {
    id: 'text-password',
    value: 'secret123',
    type: 'password',
    onChange: fn(),
  },
}

export const WithPlaceholder: Story = {
  args: {
    id: 'text-placeholder',
    value: '',
    placeholder: 'Type here...',
    onChange: fn(),
  },
}
