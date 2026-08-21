import type { Meta, StoryObj } from '@storybook/react'
import { fn } from 'storybook/test'
import type { SecretValue } from '../../lib/types'
import { SecretInput } from './SecretInput'

const meta = {
  title: 'Molecules/SecretInput',
  component: SecretInput,
  tags: ['autodocs'],
  parameters: {
    layout: 'centered',
  },
} satisfies Meta<typeof SecretInput>

export default meta
type Story = StoryObj<typeof meta>

export const LiteralMode: Story = {
  args: {
    id: 'secret-literal',
    value: { mode: 'literal', value: 'my-secret-key', has_env_var: false } satisfies SecretValue,
    onChange: fn(),
  },
}

export const EnvMode: Story = {
  args: {
    id: 'secret-env',
    value: { mode: 'env', env_name: 'API_KEY', has_env_var: true } satisfies SecretValue,
    onChange: fn(),
  },
}

export const EmptyMode: Story = {
  args: {
    id: 'secret-empty',
    value: { mode: 'empty', has_env_var: false } satisfies SecretValue,
    onChange: fn(),
  },
}

export const Disabled: Story = {
  args: {
    id: 'secret-disabled',
    value: { mode: 'literal', value: 'secret', has_env_var: false } satisfies SecretValue,
    disabled: true,
    onChange: fn(),
  },
}
