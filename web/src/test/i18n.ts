import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from '../i18n/locales/en.json'
import es from '../i18n/locales/es.json'
import pt from '../i18n/locales/pt.json'

const resources = {
  en: { translation: en },
  es: { translation: es },
  pt: { translation: pt },
}

i18n.use(initReactI18next).init({
  resources,
  lng: 'es',
  fallbackLng: 'es',
  interpolation: {
    escapeValue: false,
  },
})

export default i18n
