import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import resourcesToBackend from 'i18next-resources-to-backend'
import { initReactI18next } from 'react-i18next'

import common from '../locales/en/common.json'
import auth from '../locales/en/auth.json'
import decks from '../locales/en/decks.json'
import study from '../locales/en/study.json'
import authoring from '../locales/en/authoring.json'
import theme from '../locales/en/theme.json'
import ui from '../locales/en/ui.json'
import nav from '../locales/en/nav.json'
import stats from '../locales/en/stats.json'
import browse from '../locales/en/browse.json'
import settings from '../locales/en/settings.json'

// Supported UI locales. `en` is the source/fallback and is bundled eagerly (below) so the
// first paint never blocks on a network/chunk fetch; `ru`/`zh` catalogs are lazy-loaded on
// demand (see the resourcesToBackend backend) so their JSON only ships when a user selects
// them. Keep this list in sync with the locale switcher (features/settings) and the CJK font
// loader (lib/fonts.ts).
export const SUPPORTED_LOCALES = ['en', 'ru', 'zh'] as const
export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number]

export const defaultNS = 'common'

const namespaces = [
  'common',
  'auth',
  'decks',
  'study',
  'authoring',
  'theme',
  'ui',
  'nav',
  'stats',
  'browse',
  'settings',
]

void i18n
  .use(LanguageDetector)
  // Lazy-loads any locale/namespace not already present in `resources` (i.e. everything but
  // `en`) via a dynamic import; Vite code-splits each locale bundle into its own chunk.
  .use(resourcesToBackend((lng: string, ns: string) => import(`../locales/${lng}/${ns}.json`)))
  .use(initReactI18next)
  .init({
    // `en` bundled inline as the synchronous fallback; `partialBundledLanguages` lets the
    // backend fill in `ru`/`zh` on top without disabling the preloaded `en` resources.
    resources: {
      en: { common, auth, decks, study, authoring, theme, ui, nav, stats, browse, settings },
    },
    partialBundledLanguages: true,
    fallbackLng: 'en',
    supportedLngs: [...SUPPORTED_LOCALES],
    // Resolve regional variants (e.g. `zh-CN`, `ru-RU`, browser-reported) to their base bundle.
    load: 'languageOnly',
    nonExplicitSupportedLngs: true,
    defaultNS,
    ns: namespaces,
    interpolation: {
      escapeValue: false,
    },
    react: {
      // No Suspense boundary wraps the routes; render with the fallback bundle and re-render
      // once a lazily-loaded locale arrives, rather than suspending the tree.
      useSuspense: false,
    },
  })

// Keep <html lang> in sync with the active locale so `[lang]` CSS (the CJK font stack in
// index.css) and assistive technology track the UI language. Runs for every change and once
// for the initially-detected language.
function syncDocumentLang(): void {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = i18n.resolvedLanguage ?? 'en'
  }
}
i18n.on('languageChanged', syncDocumentLang)
i18n.on('initialized', syncDocumentLang)

export default i18n
