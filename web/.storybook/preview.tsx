import { definePreview } from '@storybook/react'
import type { Preview } from '@storybook/react'
import '../src/styles/index.css'
import { withI18n, i18nGlobalTypes } from './i18n'

const preview = definePreview({
  parameters: {
    controls: {
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    layout: 'padded',
  },
  globalTypes: {
    ...i18nGlobalTypes,
    theme: {
      name: 'Theme',
      description: 'Lele UI theme',
      defaultValue: 'dark',
      toolbar: {
        icon: 'circlehollow',
        items: [
          { value: 'dark', icon: 'circle', title: 'Dark' },
          { value: 'light', icon: 'circlehollow', title: 'Light' },
        ],
        dynamicTitle: true,
      },
    },
  },
  decorators: [
    withI18n,
    (Story, context) => {
      const theme = context.globals.theme as 'dark' | 'light'
      document.documentElement.setAttribute('data-theme', theme)
      return <Story />
    },
  ],
})

export default preview
