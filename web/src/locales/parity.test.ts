import { describe, expect, it } from 'vitest'

// Guards against silent English fallback: every key present in an `en` namespace catalog must
// also exist in `ru` and `zh`. A missing key in a target locale would render the English string
// with no visible error, so this test is the safety net for translation drift as new UI keys
// land. Extra keys in a target locale are allowed (e.g. Russian's additional `_few`/`_many`
// plural categories, which English/Chinese don't have).

type Json = Record<string, unknown>

// Eagerly load every locale namespace JSON at build time (Vite resolves the glob).
const catalogs = import.meta.glob<Json>('./*/*.json', { eager: true, import: 'default' })

// './en/browse.json' -> { locale: 'en', ns: 'browse' }
function parsePath(path: string): { locale: string; ns: string } {
  const [, locale, file] = path.split('/')
  return { locale, ns: file.replace(/\.json$/, '') }
}

function flatten(obj: Json, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([key, value]) => {
    const path = prefix ? `${prefix}.${key}` : key
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      return flatten(value as Json, path)
    }
    return [path]
  })
}

// Index catalogs by locale -> ns -> flattened key set.
const byLocale: Record<string, Record<string, Set<string>>> = {}
for (const [path, mod] of Object.entries(catalogs)) {
  const { locale, ns } = parsePath(path)
  byLocale[locale] ??= {}
  byLocale[locale][ns] = new Set(flatten(mod))
}

const TARGET_LOCALES = ['ru', 'zh'] as const
const enNamespaces = byLocale.en

describe('locale catalog key parity', () => {
  it('has an en catalog to compare against', () => {
    expect(Object.keys(enNamespaces ?? {}).length).toBeGreaterThan(0)
  })

  for (const locale of TARGET_LOCALES) {
    describe(locale, () => {
      for (const [ns, enKeys] of Object.entries(enNamespaces)) {
        it(`${ns}: covers every en key`, () => {
          const targetKeys = byLocale[locale]?.[ns]
          expect(targetKeys, `missing namespace ${locale}/${ns}.json`).toBeDefined()
          const missing = [...enKeys].filter((k) => !targetKeys!.has(k))
          expect(missing, `missing keys in ${locale}/${ns}.json`).toEqual([])
        })
      }
    })
  }
})
