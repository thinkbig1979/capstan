# Capstan E2E Testing

End-to-end browser tests for Capstan, written in [Playwright](https://playwright.dev/).

```
testing/
├── README.md
├── tests/playwright/
│   ├── auth-session.spec.ts    # Setup, login, session revocation
│   └── backup-flow.spec.ts     # Backup configure -> run -> restore
└── reports/                    # Playwright reporter output (generated, gitignored)
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

Both specs need a running backend and frontend. The environment variables they
read (base URL, API URL, test credentials, stack name, restic repo path) are
documented in the header comment of `playwright.config.ts`.

The two specs need *different* backends and cannot run against the same one:

| Spec | Backend requirement |
|------|---------------------|
| `auth-session.spec.ts` | `AUTH_DISABLED=false` and a virgin `DATA_DIR` — its first test performs the one and only `POST /auth/setup` and asserts `needsSetup` was true beforehand |
| `backup-flow.spec.ts` | `AUTH_DISABLED=true`, restic 0.19.1+ on `PATH`, a Docker daemon (the backup path stops the stack before snapshotting), and a pre-existing stack named `test-app` |

## Running in CI

`.github/workflows/e2e-backup.yml` runs both specs on every pull request, on
push to `main`, and nightly at 03:17 UTC. It uses two separate jobs with two
separate backends, routing tests by path with `--grep`/`--grep-invert`. That
workflow's header comment carries the full rationale for the split, the runner
choices, and the reporter flags; read it before changing how the suite is
invoked.

## Reports

`playwright.config.ts` configures `list`, `html`, and `json` reporters writing
to `testing/reports/`. Do not pass `--reporter` on the command line in CI: it
replaces the whole configured array, including the custom output paths, and the
report-upload steps then find nothing.

## Adding a spec

Drop a `*.spec.ts` file into `testing/tests/playwright/` and it is picked up
automatically — no workflow edit needed. Check which of the two CI jobs it
belongs to first: a new spec runs in the backup-flow job by default, since that
job selects everything except `auth-session`. A spec needing real authentication
must get its own job with its own isolated backend.
