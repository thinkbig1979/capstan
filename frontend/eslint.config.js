import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    rules: {
      // Underscore-prefixed params/vars mark intentionally-unused bindings
      // (e.g. mock function signatures that must match a call shape).
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
    },
  },
  {
    files: ['src/components/ui/*.tsx'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
  // agent-os-5g8a: a ratchet over the SHAPE that lets the backend's cause be
  // discarded — a fixed string, template or `x || 'fixed string'` as the first
  // argument to toast.error. Deliberately WIDER than the defect class: a hit is
  // "this call decided the wording without consulting the rejection", which is
  // usually a bug and sometimes just the house idiom written the long way.
  // Either way the answer is one of the three presenters in error-handler.ts,
  // which is why that file (and only that file) is exempt.
  //
  // `no-restricted-syntax` is configured NOWHERE else in this file; flat config
  // REPLACES rule options rather than merging them, so a second object carrying
  // this rule would silently discard these selectors.
  {
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/lib/error-handler.ts'],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector:
            "CallExpression[callee.object.name='toast'][callee.property.name='error'] > Literal:first-child",
          message: 'Use presentError(err, { fallback }) so the backend cause is shown.',
        },
        {
          selector:
            "CallExpression[callee.object.name='toast'][callee.property.name='error'] > TemplateLiteral:first-child",
          message: 'Use presentError(err, { fallback }) so the backend cause is shown.',
        },
        {
          selector:
            "CallExpression[callee.object.name='toast'][callee.property.name='error'] > LogicalExpression:first-child",
          message: 'Use presentError(err, { fallback }) so the backend cause is shown.',
        },
      ],
    },
  },
])
