import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineMain } from '@storybook/react-vite/node'

const dirname = path.dirname(fileURLToPath(import.meta.url))

export default defineMain({
  stories: [
    '../src/**/*.stories.@(ts|tsx|mdx)',
  ],
  addons: [
    '@storybook/addon-themes',
  ],
  framework: {
    name: '@storybook/react-vite',
    options: {},
  },
  staticDirs: ['../public'],
  viteConfig: {
    resolve: {
      alias: {
        '@': path.resolve(dirname, '../src'),
      },
    },
  },
  docs: {
    autodocs: 'tag',
  },
})
