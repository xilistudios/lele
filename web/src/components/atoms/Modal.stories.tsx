import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { Modal } from './Modal'

const meta = {
  title: 'Atoms/Modal',
  component: Modal,
  tags: ['autodocs'],
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    isOpen: true,
    onClose: fn(),
    children: (
      <div className="p-6">
        <p className="text-sm text-text-secondary">
          This is the modal content area. You can put anything here.
        </p>
      </div>
    ),
  },
} satisfies Meta<typeof Modal>

export default meta
type Story = StoryObj<typeof meta>

export const Small: Story = {
  args: {
    title: 'Small Modal',
    size: 'sm',
  },
}

export const Medium: Story = {
  args: {
    title: 'Medium Modal',
    size: 'md',
  },
}

export const Large: Story = {
  args: {
    title: 'Large Modal',
    size: 'lg',
  },
}

export const ExtraLarge: Story = {
  args: {
    title: 'Extra Large Modal',
    size: 'xl',
  },
}

export const Full: Story = {
  args: {
    title: 'Full Width Modal',
    size: 'full',
  },
}

export const NoCloseButton: Story = {
  args: {
    title: 'No Close Button',
    showCloseButton: false,
  },
}
