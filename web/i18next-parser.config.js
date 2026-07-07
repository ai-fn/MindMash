// i18next-parser config for the `npm run i18n:extract` developer workflow. Scans the source for
// `t()` / `useTranslation(ns)` usage and syncs the *English source* catalogs under
// src/locales/en/. Target locales (ru, zh) are maintained separately and guarded by the
// src/locales/parity.test.ts key-parity test — the parser deliberately does not touch them.
//
// `keepRemoved: true` keeps the extraction additive/non-destructive: keys the static analyzer
// can't see (e.g. dynamic `t(`localeNames.${code}`)`) are preserved rather than pruned. Run it
// after adding UI strings, then fill in the ru/zh values.
export default {
  locales: ['en'],
  defaultNamespace: 'common',
  input: ['src/**/*.{ts,tsx}'],
  output: 'src/locales/$LOCALE/$NAMESPACE.json',
  keySeparator: '.',
  namespaceSeparator: false,
  keepRemoved: true,
  sort: true,
  createOldCatalogs: false,
}
