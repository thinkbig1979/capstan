import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

// agent-os-z91e.42: a directive that silences react-hooks/exhaustive-deps must
// say why. That rule is the one that would have flagged a long-lived instance
// (xterm, CodeMirror) rebuilt by an effect whose deps change during its life,
// and a bare disable hid exactly that twice (agent-os-z91e.34, z91e.41).
//
// A local rule rather than eslint-plugin-eslint-comments: no new dependency,
// and it rides on `pnpm lint`, which the required Frontend CI check already runs.
//
// Covers every directive form ESLint honours (`eslint-disable-next-line`,
// `eslint-disable-line`, and the block `/* eslint-disable */`, which may be a
// disable-line/-next-line too), a rule list naming exhaustive-deps among
// others, and a blanket disable with no rule list (it silences the rule too).
// The reason follows ESLint's own ` -- ` separator and must hold at least
// MIN_REASON_CHARS non-space characters, which rejects "-- todo" and
// "-- reason" but is not a quality check: a reviewer still reads the reason.
//
// Known gap: a blanket `/* eslint-disable */` that starts at line 1 column 0
// cannot be reported, because it silences this rule from the first position
// on. Mid-file blankets are caught. (Measured with planted files, see the
// agent-os-z91e.42 close.)
const MIN_REASON_CHARS = 10
const EXHAUSTIVE_DEPS = 'react-hooks/exhaustive-deps'

const localPlugin = {
  rules: {
    'exhaustive-deps-disable-needs-reason': {
      meta: {
        type: 'problem',
        schema: [],
        messages: {
          missingBlanket: `The blanket eslint-disable at line {{line}} silences ${EXHAUSTIVE_DEPS}: name the rules it disables and end it with " -- <reason>" of at least ${MIN_REASON_CHARS} non-space characters.`,
          missing: `A disable of ${EXHAUSTIVE_DEPS} must end with " -- <reason>" of at least ${MIN_REASON_CHARS} non-space characters saying why the dependency list is correct as written.`,
        },
      },
      create(context) {
        return {
          Program() {
            for (const comment of context.sourceCode.getAllComments()) {
              const match = /^\s*eslint-disable(-next-line|-line)?(?=\s|$)([\s\S]*)$/.exec(
                comment.value,
              )
              if (!match) continue
              // `// eslint-disable` (no -line) is not a directive in a line comment.
              if (comment.type === 'Line' && !match[1]) continue
              const [head, ...reason] = match[2].split(/(?:^|\s)-{2,}(?:\s|$)/)
              const rules = head
                .split(',')
                .map((r) => r.trim())
                .filter(Boolean)
              if (rules.length > 0 && !rules.includes(EXHAUSTIVE_DEPS)) continue
              if (reason.join(' ').replace(/\s/g, '').length >= MIN_REASON_CHARS) continue
              // A block disable silences problems from its own position onward,
              // this rule's included, so a blanket one is reported at the top of
              // the file instead (before the directive takes effect), naming
              // its line.
              const blanket = rules.length === 0
              context.report({
                loc: blanket
                  ? { start: { line: 1, column: 0 }, end: { line: 1, column: 1 } }
                  : comment.loc,
                messageId: blanket ? 'missingBlanket' : 'missing',
                data: { line: String(comment.loc.start.line) },
              })
            }
          },
        }
      },
    },
  },
}

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
    files: ['**/*.{ts,tsx}'],
    plugins: { local: localPlugin },
    rules: { 'local/exhaustive-deps-disable-needs-reason': 'error' },
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
        // agent-os-lurn: the SAME defect written with the query's `error`
        // OBJECT instead of the identifier `isError`. Every selector above
        // keys on the NAME `isError`, so all eight were structurally blind to
        // this spelling and four live sites sat under them (DashboardPage,
        // StackPage, DiffViewer, AuditLogContent).
        //
        // `:has(JSXElement)` is the whole discriminator and is NOT cosmetic.
        // `error` is also the name of validation strings in this codebase --
        // useCreateStackSubmit's handleCreate() does `const error =
        // validateName(name)` then `if (error)` -- and that guard's body is
        // setNameError/toastInvalid/return, with no JSX in it. MEASURED before
        // shipping, with a deliberately WIDE control selector carrying no
        // `:has`: the control fired on useCreateStackSubmit and these two did
        // not, so the miss is discrimination and not blindness. Distinguishing
        // a query error from a validation string needs no type information,
        // only the shape of what the guard RETURNS.
        //
        // No ConditionalExpression pair here, for the reason given above: a
        // `const cause = error ? … : null` inside an already-decided guard is
        // correct code and appears at three of the four sites.
        {
          selector: "IfStatement[test.type='Identifier'][test.name='error']:has(JSXElement)",
          message:
            'A query `error` object is set when a REFETCH fails as well as on a first load, and TanStack keeps the data in both cases. Guard on error && !data (or isLoadingError) and report the failure with RefreshFailedNotice, so a failed refresh does not discard a populated view.',
        },
        {
          selector:
            "IfStatement[test.type='LogicalExpression'][test.operator='||'][test.left.type='Identifier'][test.left.name='error']:has(JSXElement)",
          message:
            'Drop the `error` disjunct: !data already covers "failed and never loaded", and `error ||` also fires on a REFETCH failure over data TanStack still holds, discarding a populated view. Report the failure with RefreshFailedNotice instead.',
        },
        // agent-os-qags.4 (rule 9): an empty numeric settings field must never
        // become a number. `parseInt('') || 0` is NaN || 0, so a cleared box saved
        // 0 -- "disabled" or "keep none" on most of these screens -- and `|| 4`
        // saved the engine default the operator never chose (agent-os-z91e.15).
        // Keyed on the `||`/`??` NODE whose immediate left is the parse call and
        // whose right is a numeric literal, so it matches however the callee is
        // spelled (parseInt, Number, parseFloat, Number.parseInt) and whatever
        // the literal is. NumericField is the one place that maps text to
        // number | null.
        {
          selector:
            "LogicalExpression[operator=/^(\\|\\||\\?\\?)$/][right.type='Literal'][right.value=type(number)]:matches([left.callee.name=/^(parseInt|parseFloat|Number)$/], [left.callee.property.name=/^(parseInt|parseFloat)$/])",
          message:
            'An empty numeric field must not become a number. Use NumericField (components/settings/NumericField.tsx) and hold number | null in the draft, or parseWholeNumber, and block Save while it is null.',
        },
        // agent-os-7nqa: a first-load gate keyed on `isLoading`. TanStack's
        // isLoading is `isPending && isFetching`, and a first fetch started
        // while the browser is offline is 'paused', so isLoading is false with
        // no data and the component falls through to an error, "not found" or
        // empty-state claim about a read that never ran (the settings pages,
        // agent-os-cg26; every dashboard and stack tab, this bead). Use
        // `isPending`, or `isFirstLoad({ isPending, fetchStatus })`
        // (lib/query-state.ts) for a query with `enabled: ...`, which plain
        // isPending would spin on forever while it is disabled.
        //
        // Keyed on the DESTRUCTURE of a call result (`const { isLoading } =
        // useX()`, renamed or not) and on a member read of it
        // (`result.isLoading`). Props destructuring (`({ isLoading })`, a
        // presentational flag) and the useState pair are different shapes and do
        // not match. KNOWN GAP: a destructure from something that is not a call
        // (`const { isLoading } = data`, where the query result arrives as a
        // prop) is invisible here; the call that produced it is the site.
        {
          selector:
            "VariableDeclarator[init.type='CallExpression'] > ObjectPattern > Property[key.name='isLoading']",
          message:
            'isLoading is false while a first fetch is paused offline, so this gate falls through to an error or empty state. Use isPending, or isFirstLoad({ isPending, fetchStatus }) from lib/query-state for a query with `enabled`.',
        },
        {
          selector: "MemberExpression[computed=false][property.name='isLoading']",
          message:
            'isLoading is false while a first fetch is paused offline, so this gate falls through to an error or empty state. Read isPending, or isFirstLoad({ isPending, fetchStatus }) from lib/query-state for a query with `enabled`.',
        },
        // agent-os-z91e.12: a raw <a> with a same-origin path is a full document
        // navigation: the SPA unloads, taking the query cache, every open
        // WebSocket and the in-memory env-unlock state with it. Internal links
        // use react-router's <Link to>. Keyed on the href's FIRST character so an
        // external https: link (docs, releases, published ports) never matches;
        // the three spellings are a string literal, a braced literal and a
        // template literal, which is how all four fixed sites were written.
        {
          selector:
            "JSXOpeningElement[name.name='a'] > JSXAttribute[name.name='href'][value.type='Literal'][value.value=/^\\//]",
          message:
            "A same-origin href on a raw <a> reloads the SPA and drops the query cache, sockets and env-unlock state. Use <Link to> from 'react-router'.",
        },
        {
          selector:
            "JSXOpeningElement[name.name='a'] > JSXAttribute[name.name='href'] > JSXExpressionContainer > Literal[value=/^\\//]",
          message:
            "A same-origin href on a raw <a> reloads the SPA and drops the query cache, sockets and env-unlock state. Use <Link to> from 'react-router'.",
        },
        {
          selector:
            "JSXOpeningElement[name.name='a'] > JSXAttribute[name.name='href'] > JSXExpressionContainer > TemplateLiteral[quasis.0.value.raw=/^\\//]",
          message:
            "A same-origin href on a raw <a> reloads the SPA and drops the query cache, sockets and env-unlock state. Use <Link to> from 'react-router'.",
        },
      ],
    },
  },
])
