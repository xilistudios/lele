import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import type { Decorator } from '@storybook/react'
import en from '../src/i18n/locales/en.json'
import es from '../src/i18n/locales/es.json'
import pt from '../src/i18n/locales/pt.json'

// Initialize i18next once for all stories
if (!i18n.isInitialized) {
  i18n.use(initReactI18next).init({
    resources: {
      en: { translation: en },
      es: { translation: es },
      pt: { translation: pt },
    },
    lng: 'en',
    fallbackLng: 'en',
    interpolation: {
      escapeValue: false,
    },
  })
}

/**
 * Storybook decorator that wraps stories with i18next support.
 * Also provides a language switcher in the toolbar.
 */
export const withI18n: Decorator = (Story, context) => {
  const lang = context.globals.locale as string
  if (lang && lang !== i18n.language) {
    i18n.changeLanguage(lang)
  }
  return <Story />
}

// Extend global types for locale
declare module '@storybook/react' {
  interface ProjectAnnotations {
    globalTypes?: Record<string, unknown>
  }
}

export const i18nGlobalTypes = {
  locale: {
    name: 'Locale',
    description: 'Interface language',
    defaultValue: 'en',
    toolbar: {
      icon: 'globe',
      items: [
        { value: 'en', title: 'English' },
        { value: 'es', title: 'Español' },
        { value: 'pt', title: 'Português' },
      ],
      dynamicTitle: true,
    },
  },
}
