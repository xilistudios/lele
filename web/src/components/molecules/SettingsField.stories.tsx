import type { Meta, StoryObj } from '@storybook/react'
import { SettingsField } from './SettingsField'

const meta = {
  title: 'Molecules/SettingsField',
  component: SettingsField,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof SettingsField>

export default meta
type Story = StoryObj<typeof meta>

const inputCls =
  'w-full rounded border border-border bg-background-primary px-3 py-2 text-sm text-text-primary'

export const Default: Story = {
  args: {
    label: 'API URL',
    path: 'api.url',
    children: <input className={inputCls} />,
  },
}

export const WithDescription: Story = {
  args: {
    label: 'API URL',
    path: 'api.url',
    description: 'The URL of the Lele API server',
    children: <input className={inputCls} />,
  },
}

export const WithError: Story = {
  args: {
    label: 'API URL',
    path: 'api.url',
    error: 'Invalid URL format',
    children: <input className={inputCls} />,
  },
}

export const IsDirty: Story = {
  args: {
    label: 'API URL',
    path: 'api.url',
    isDirty: true,
    children: <input className={inputCls} defaultValue="http://localhost:3636" />,
  },
}

export const Required: Story = {
  args: {
    label: 'API URL',
    path: 'api.url',
    required: true,
    children: <input className={inputCls} />,
  },
}
