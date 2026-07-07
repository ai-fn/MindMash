import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import i18next from 'eslint-plugin-i18next'
import tseslint from 'typescript-eslint'
import prettier from 'eslint-config-prettier'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores([
    'dist',
    'coverage',
    'playwright-report',
    'test-results',
    // Generated orval client only (mode: tags-split outputs an endpoints.ts + model
    // files per service) — NOT mutator.ts/authToken.ts, which are hand-written and
    // must stay linted. Kept in tsconfig typecheck (not excluded there) so type drift
    // is still caught; only ESLint and prettier skip this generated code.
    'src/lib/api/identity',
    'src/lib/api/collection',
    'src/lib/api/scheduler',
    'src/lib/api/sync',
  ]),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
      prettier,
    ],
    languageOptions: {
      globals: globals.browser,
    },
  },
  {
    // Node-context config files (vite.config.ts, playwright.config.ts, etc.) run under
    // Node, not the browser — they reference `process`, `__dirname`, etc.
    files: ['*.config.ts', '*.config.js'],
    languageOptions: {
      globals: globals.node,
    },
  },
  {
    // i18n hardcoded-string gate (spec 13 build step 1): user-visible JSX text in the app's
    // screens/components must go through `t()`, never be a literal. `jsx-text-only` mode checks
    // only text between JSX tags — attribute/prop strings (className, aria-*, keys) are left to
    // developers, so this flags real untranslated copy without drowning in false positives.
    // Scoped to the rendered surfaces; tests and generated clients are excluded.
    files: ['src/features/**/*.tsx', 'src/ui/**/*.tsx', 'src/app/**/*.tsx'],
    ignores: ['**/*.test.tsx', '**/*.test.ts'],
    plugins: { i18next },
    rules: {
      'i18next/no-literal-string': ['error', { mode: 'jsx-text-only' }],
    },
  },
])
