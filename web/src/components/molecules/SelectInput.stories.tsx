import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import { SelectInput } from './SelectInput'

const meta = {
  title: 'Molecules/SelectInput',
  component: SelectInput,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof SelectInput>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: {
    id: 'select-1',
    value: 'option1',
    options: [
      { value: 'option1', label: 'Option 1' },
      { value: 'option2', label: 'Option 2' },
      { value: 'option3', label: 'Option 3' },
    ],
    onChange: fn(),
  },
}

export const WithManyOptions: Story = {
  args: {
    id: 'select-many',
    value: 'option5',
    options: Array.from({ length: 10 }, (_, i) => ({
      value: `option${i + 1}`,
      label: `Option ${i + 1}`,
    })),
    onChange: fn(),
  },
}

export const Disabled: Story = {
  args: {
    id: 'select-disabled',
    value: 'option1',
    options: [
      { value: 'option1', label: 'Option 1' },
      { value: 'option2', label: 'Option 2' },
      { value: 'option3', label: 'Option 3' },
    ],
    disabled: true,
    onChange: fn(),
  },
}
