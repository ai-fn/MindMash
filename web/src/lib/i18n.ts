import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import common from '../locales/en/common.json'

// MVP scope: English only. The JWT `locale` claim will drive the active
// language once auth lands (Task 3); additional locale bundles + RTL/CJK
// support are deferred to Phase 2 (see plan.md Global Constraint 5/8).
export const defaultNS = 'common'

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      en: { common },
    },
    fallbackLng: 'en',
    supportedLngs: ['en'],
    defaultNS,
    ns: ['common'],
    interpolation: {
      escapeValue: false,
    },
  })

export default i18n
