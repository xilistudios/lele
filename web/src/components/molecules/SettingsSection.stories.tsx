import type { Meta, StoryObj } from '@storybook/react'
import { SettingsSection } from './SettingsSection'

const meta = {
  title: 'Molecules/SettingsSection',
  component: SettingsSection,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof SettingsSection>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    title: 'General Settings',
    children: <p className="text-sm text-text-secondary">Settings content</p>,
  },
}

export const WithDescription: Story = {
  args: {
    title: 'General Settings',
    description: 'Configure general application settings',
    children: <p className="text-sm text-text-secondary">Settings content</p>,
  },
}

export const RequiresRestart: Story = {
  args: {
    title: 'General Settings',
    isRestartRequired: true,
    children: <p className="text-sm text-text-secondary">Settings content</p>,
  },
}
