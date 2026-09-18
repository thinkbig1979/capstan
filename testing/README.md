# Capstan E2E Testing

End-to-end browser tests for Capstan, written in [Playwright](https://playwright.dev/).

```
testing/
├── README.md
├── tests/playwright/
│   ├── helpers/
│   │   └── network-settle.ts        # Settle guard used instead of networkidle waits
│   ├── auth-session.spec.ts         # Setup, login, session revocation
│   ├── backup-flow.spec.ts          # Backup configure -> run -> restore
│   ├── dashboard-backups.spec.ts    # Dashboard Backups tab + Auto backup toggle
│   ├── network-settle-guard.spec.ts # Regression controls for helpers/network-settle.ts
│   └── terminal-flow.spec.ts        # Start stack -> open shell -> run a command
└── reports/                         # Playwright reporter output (generated, gitignored)
```

## Running locally

Specs are driven by the repo-root `playwright.config.ts`, which sets
`testDir: './testing/tests/playwright'` and discovers every `*.spec.ts` under it.

```bash
pnpm install --frozen-lockfile
npx playwright install --with-deps chromium

npx playwright test                    # all specs
npx playwright test backup-flow        # one spec
npx playwright test --reporter=line    # compact output
```

Four of the five specs need a running backend and frontend;
`network-settle-guard.spec.ts` needs neither. The environment variables the
browser specs read (base URL, API URL, test credentials, stack name, restic
repo path) are documented in the header comment of `playwright.config.ts`.

The specs split across *two* backends, which cannot be the same one:

| Spec | Backend requirement |
|------|---------------------|
| `auth-session.spec.ts` | `AUTH_DISABLED=false` and a virgin `DATA_DIR` — its first test performs the one and only `POST /auth/setup` and asserts `needsSetup` was true beforehand |
| `backup-flow.spec.ts` | `AUTH_DISABLED=true`, restic 0.18.0+ on `PATH`, a Docker daemon (the backup path stops the stack before snapshotting), and a pre-existing stack named `test-app` |
| `dashboard-backups.spec.ts` | The same backend as `backup-flow.spec.ts`: `AUTH_DISABLED=true`, restic 0.18.0+ on `PATH`, a Docker daemon, and a pre-existing `test-app` stack. Runs in the backup-flow CI job. Documented with `--retries=0`, because `playwright.config.ts` retries once under CI and Playwright reports a fail-then-pass as FLAKY, not as passed |
| `network-settle-guard.spec.ts` | None — no backend, no frontend, no browser. Its eight tests drive a stub page object to assert the settle guard in `helpers/network-settle.ts` still refuses the orders that defeated an earlier revision of it. Runs in the backup-flow CI job because that job selects everything except `auth-session`, not because it needs that job's backend |
| `terminal-flow.spec.ts` | `AUTH_DISABLED=true` and a Docker daemon — it starts the `test-app` stack itself and opens a real shell into its container. Runs in the backup-flow CI job (same backend) |

## Running in CI

`.github/workflows/e2e-backup.yml` runs the suite on every pull request, on push
to `main`, and nightly at 03:17 UTC. It uses two jobs with two separate backends
and routes tests by path, not by tag:

| Job | Command | Selects |
|-----|---------|---------|
| `backup-flow` (`AUTH_DISABLED=true`) | `npx playwright test --grep-invert "auth-session"` | Every spec except `auth-session.spec.ts` |
| `auth-session` (`AUTH_DISABLED=false`) | `npx playwright test --grep "auth-session"` | `auth-session.spec.ts` only |

**Neither command names a spec file, so both auto-discover.** `--grep-invert`
and `--grep` match against the full test title, which includes the file path;
Playwright itself enumerates `testDir`. A new spec is therefore picked up by the
`backup-flow` job with no workflow edit at all. That is convenient and it is
also exactly why this document rotted: `dashboard-backups.spec.ts` and
`network-settle-guard.spec.ts` both entered CI without a single diff line that
would have prompted anyone to update the tables above. Nothing enforces the
match between this file and `testing/tests/playwright/` — only the habit of
editing them together.

That workflow's header comment carries the full rationale for the split, the
runner choices, and the reporter flags; read it before changing how the suite is
invoked.

## Reports

`playwright.config.ts` configures `list`, `html`, and `json` reporters writing
to `testing/reports/`. Do not pass `--reporter` on the command line in CI: it
replaces the whole configured array, including the custom output paths, and the
report-upload steps then find nothing.

## Adding a spec

Drop a `*.spec.ts` file into `testing/tests/playwright/` and it is picked up
automatically — no workflow edit needed. Check which of the two CI jobs it
belongs to first: a new spec runs in the `backup-flow` job by default, since
that job selects everything except `auth-session`. A spec needing real
authentication must get its own job with its own isolated backend.

Then add it to the file tree and the backend-requirement table above. Nothing
checks that for you.
