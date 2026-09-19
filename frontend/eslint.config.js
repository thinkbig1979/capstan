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
        // agent-os-wczm: a ratchet over a query-error guard keyed on a BARE
        // `isError`. `isError` is true when a REFETCH fails as well as when the
        // first load fails, and staleTime is 30s with refetchOnWindowFocus on
        // and no auto-retry for a 500 — so a guard that cannot tell the two
        // apart replaces a populated view (and any unsaved edits in it) with an
        // error box after one tab-away.
        //
        // The last two selectors key on the `||` NODE rather than on the
        // IfStatement's test: `isError || !a || !b` parses as
        // `(isError || !a) || !b`, so `test.left` is a LogicalExpression and an
        // IfStatement-anchored selector misses it. Keying on any `||` whose
        // IMMEDIATE left is isError catches it at any depth.
        //
        // Deliberately NO ConditionalExpression selectors: a bare-test one
        // fires on `const cause = isError ? … : null` inside a guard that has
        // already decided, which is correct code.
        {
          selector: "IfStatement[test.type='Identifier'][test.name='isError']",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Use isLoadingError, or isError && !data, so a failed refresh does not discard a populated view.',
        },
        {
          selector: "IfStatement[test.type='MemberExpression'][test.property.name='isError']",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Use isLoadingError, or isError && !data, so a failed refresh does not discard a populated view.',
        },
        {
          selector:
            "LogicalExpression[operator='||'][left.type='Identifier'][left.name='isError']",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Drop the isError disjunct — !data already covers "failed and never loaded" — or use isLoadingError.',
        },
        {
          selector:
            "LogicalExpression[operator='||'][left.type='MemberExpression'][left.property.name='isError']",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Drop the isError disjunct — !data already covers "failed and never loaded" — or use isLoadingError.',
        },
        // The MIRROR of the two above. `isError || !data` was caught and
        // `!data || isError` was not, which is a one-token evasion of a rule
        // whose whole job is to stop this shape coming back.
        {
          selector:
            "LogicalExpression[operator='||'][right.type='Identifier'][right.name='isError']",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Drop the isError disjunct — !data already covers "failed and never loaded" — or use isLoadingError.',
        },
        {
          selector:
            "LogicalExpression[operator='||'][right.type='MemberExpression'][right.property.name='isError']",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Drop the isError disjunct — !data already covers "failed and never loaded" — or use isLoadingError.',
        },
        // The TERNARY form, narrowed to one that renders. DockerCleanupCard's
        // run-history guard was exactly this shape and was missed when the bead
        // was filed, precisely because it is invisible to an `if (isError` read.
        // `:has(JSXElement)` is what keeps it off `const cause = isError ? … :
        // null` (BackupSettingsContent), which is correct code: that ternary
        // picks a STRING inside a guard that has already fired, and has no JSX
        // under it. A bare-test ConditionalExpression selector fires on it and
        // must not be used.
        {
          selector:
            "ConditionalExpression[test.type='Identifier'][test.name='isError']:has(JSXElement)",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Render the error view on isLoadingError, or isError && !data, so a failed refresh does not replace a populated one.',
        },
        {
          selector:
            "ConditionalExpression[test.type='MemberExpression'][test.property.name='isError']:has(JSXElement)",
          message:
            'A bare isError is also true when a REFETCH fails on a query that already has data. Render the error view on isLoadingError, or isError && !data, so a failed refresh does not replace a populated one.',
        },
      ],
    },
  },
])
