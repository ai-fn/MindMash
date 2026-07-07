// Locale selection policy shared by the auth store (server-reported locale) and the Settings
// locale switcher (explicit user choice). Precedence: an explicit switcher choice always wins
// over the backend-reported `locale`, which in turn wins over the browser default. The choice
// is persisted client-side only — Identity currently exposes no locale-update endpoint, so a
// switch does not yet survive across devices (follow-up: persist server-side once such an
// endpoint lands).
import i18n, { SUPPORTED_LOCALES, type SupportedLocale } from './i18n'

// Separate from i18next-browser-languagedetector's own `i18nextLng` cache: the detector caches
// even a passively-detected browser language, so we can't use it to tell "the user explicitly
// chose" from "we defaulted to their browser locale". This key is written only by chooseLocale.
const CHOICE_KEY = 'mm.localeChoice'

export function isSupportedLocale(value: string | null | undefined): value is SupportedLocale {
  return !!value && (SUPPORTED_LOCALES as readonly string[]).includes(value)
}

// Normalize a possibly-regional tag ('zh-CN', 'ru-RU', 'en-US') to a supported base locale.
export function normalizeLocale(tag: string | null | undefined): SupportedLocale | undefined {
  if (!tag) return undefined
  const base = tag.toLowerCase().split('-')[0]
  return isSupportedLocale(base) ? base : undefined
}

function readChoice(): SupportedLocale | undefined {
  try {
    return normalizeLocale(localStorage.getItem(CHOICE_KEY))
  } catch {
    return undefined
  }
}

// Explicit user selection from the locale switcher: persist and switch immediately.
export async function chooseLocale(locale: SupportedLocale): Promise<void> {
  try {
    localStorage.setItem(CHOICE_KEY, locale)
  } catch {
    // Storage may be unavailable (private mode / disabled) — still switch for this session.
  }
  await i18n.changeLanguage(locale)
}

// Server-reported locale (from GetMe): adopt only if the user hasn't made an explicit choice,
// so a returning user's picked language isn't clobbered by their stored account locale.
export async function applyServerLocale(tag: string | null | undefined): Promise<void> {
  if (readChoice()) return
  const locale = normalizeLocale(tag)
  if (locale && locale !== i18n.resolvedLanguage) {
    await i18n.changeLanguage(locale)
  }
}
