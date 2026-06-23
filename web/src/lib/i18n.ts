import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import common from '../locales/en/common.json'
import auth from '../locales/en/auth.json'
import decks from '../locales/en/decks.json'
import study from '../locales/en/study.json'
import authoring from '../locales/en/authoring.json'

// MVP scope: English only. The backend-reported `locale` (from GetMe, see
// features/auth/authStore.ts) will drive the active language once full locale
// switching lands; additional locale bundles + RTL/CJK support are deferred to
// Phase 2 (see plan.md Global Constraint 5/8).
export const defaultNS = 'common'

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      en: { common, auth, decks, study, authoring },
    },
    fallbackLng: 'en',
    supportedLngs: ['en'],
    defaultNS,
    ns: ['common', 'auth', 'decks', 'study', 'authoring'],
    interpolation: {
      escapeValue: false,
    },
  })

export default i18n
