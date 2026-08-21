import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { IconButton } from './IconButton'
import { EditIcon, PlusIcon, SettingsIcon, TrashIcon } from './Icons'

const meta = {
  title: 'Atoms/IconButton',
  component: IconButton,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
  args: {
    onClick: fn(),
  },
} satisfies Meta<typeof IconButton>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    children: <PlusIcon />,
    ariaLabel: 'Add',
  },
}

export const Nav: Story = {
  args: {
    variant: 'nav',
    children: <SettingsIcon />,
    ariaLabel: 'Settings',
  },
}

export const NavFull: Story = {
  args: {
    variant: 'nav-full',
    children: <SettingsIcon />,
    ariaLabel: 'Settings',
  },
  render: (args) => (
    <div className="w-48">
      <IconButton {...args}>
        <SettingsIcon />
        <span className="text-sm">Settings</span>
      </IconButton>
    </div>
  ),
}

export const Danger: Story = {
  args: {
    variant: 'danger',
    children: <TrashIcon />,
    ariaLabel: 'Delete',
  },
}

export const Ghost: Story = {
  args: {
    variant: 'ghost',
    children: <EditIcon />,
    ariaLabel: 'Edit',
  },
}

export const Disabled: Story = {
  args: {
    variant: 'default',
    children: <PlusIcon />,
    ariaLabel: 'Add',
    disabled: true,
  },
}
