/**
 * Commit and push from the UI (agent-os-k81p.4, spec criterion 19): the one
 * happy path, driven in a real browser against a real git remote.
 *
 *   edit compose -> Save -> toast "Commit and push" -> dialog (compose file
 *   pre-ticked) -> message -> "Commit and push" -> the commit is on the remote.
 *
 * The failure classes (push off, no identity, remote ahead, env confirm, ...)
 * are covered by the Go tests for criteria 1-15 and are not repeated here.
 *
 * Prerequisites (same backend as backup-flow.spec.ts):
 *   - App on CAPSTAN_BASE_URL, backend on CAPSTAN_API_URL.
 *   - AUTH_DISABLED=true (backend and this process).
 *   - git on PATH, and a backend that shares this process's filesystem (not the dev
 *     container): the stack folder is under its stacks directory and the bare remote
 *     is in this process's temp dir, and both must be visible to the backend.
 * No Docker daemon: the stack is never started.
 *
 * FIXTURE, built here rather than seeded by the workflow. CI does not export
 * STACKS_DIR to the Playwright step, so the stacks directory is read from
 * GET /settings/config. The stack folder is a clone of a bare remote in a temp
 * dir, so the folder is itself the repository (the push switch is per stack
 * folder, backend/internal/services/git_commit.go). A rescan registers it.
 *
 * CLEANUP, in afterAll: DELETE the stack (the clone's .git counts as collateral, so
 * the request carries confirmCollateral=true and its status is asserted), remove
 * the folder and the temp dir, rescan, then assert the stack row is gone. The author
 * settings are put back when they had a value. PUT /settings/git reads "" as "no
 * change", so an author that was unset STAYS set (afterAll warns). CI starts from a
 * fresh DATA_DIR, where nothing else reads it.
 */

import { test, expect, APIRequestContext } from 'playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'

const STACK_NAME = 'git-commit-push-pw'
const COMPOSE_FILE = 'compose.yaml'
// The same lint-clean shape as backup-flow's test-app: a lint warning raises a second
// toast that stacks over the "saved" one and keeps its action button from being stable.
const SEED_COMPOSE =
  'services:\n  web:\n    image: public.ecr.aws/docker/library/nginx:1.25-alpine\n    restart: unless-stopped\n'
const EDIT_MARKER = '# edited by git-commit-push e2e'
const COMMIT_MESSAGE = 'Note the e2e edit in compose'
const AUTHOR_NAME = 'Capstan E2E'
const AUTHOR_EMAIL = 'e2e@example.invalid'

let csrfToken = ''
let tempRoot = ''
let bareDir = ''
let stackFolder = ''
let stackId = ''
// The author settings before this spec, to put back (see the header).
let priorAuthor: { name: string; email: string } | null = null

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** The CSRF double-submit token: any GET sets `capstan_csrf`, mutations echo it. */
async function ensureCsrf(request: APIRequestContext): Promise<void> {
  await request.get(`${API_URL}/api/v1/stacks`)
  const state = await request.storageState()
  const cookie = state.cookies.find((c) => c.name === 'capstan_csrf')
  if (cookie?.value) csrfToken = cookie.value
}

const mutationHeaders = () => ({ 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken })

interface StackRow {
  id: string
  directory: string
  projectName?: string
  name?: string
}

async function listStacks(request: APIRequestContext): Promise<StackRow[]> {
  const resp = await request.get(`${API_URL}/api/v1/stacks`)
  expect(resp.status(), 'GET /api/v1/stacks').toBe(200)
  const body = await resp.json()
  return Array.isArray(body) ? body : (body.stacks ?? [])
}

const findFixtureStack = (stacks: StackRow[]) =>
  stacks.find((s) => (s.projectName ?? s.name) === STACK_NAME)

async function rescan(request: APIRequestContext): Promise<void> {
  const resp = await request.post(`${API_URL}/api/v1/directories/scan`, { headers: mutationHeaders() })
  expect(resp.ok(), `POST /directories/scan returned ${resp.status()}`).toBe(true)
}

/** Run git with an author for the seed commit; Capstan's own commit uses the settings. */
function git(cwd: string, ...args: string[]): string {
  return execFileSync(
    'git',
    ['-c', 'user.name=Seed', '-c', 'user.email=seed@example.invalid', ...args],
    { cwd, encoding: 'utf8' },
  ).trim()
}

/** Read the bare remote, never the clone: the clone is what Capstan wrote to. */
const remote = (...args: string[]): string =>
  execFileSync('git', [`--git-dir=${bareDir}`, ...args], { encoding: 'utf8' }).trim()

/**
 * Remove the stack (when it exists), its folder and the temp dir, then assert the
 * row is gone. No row is fine: it is the normal state before the first run.
 */
async function removeFixture(request: APIRequestContext): Promise<void> {
  const row = findFixtureStack(await listStacks(request))
  let deleteStatus: number | null = null
  if (row) {
    // .git is collateral to the delete, which answers 428 STACK_DELETE_COLLATERAL
    // without confirmCollateral=true (backend/internal/handlers/stack_crud.go).
    const resp = await request.delete(
      `${API_URL}/api/v1/stacks/${encodeURIComponent(row.id)}?confirm=true&confirmCollateral=true`,
      { headers: mutationHeaders() },
    )
    deleteStatus = resp.status()
  }
  if (stackFolder) rmSync(stackFolder, { recursive: true, force: true })
  if (tempRoot) rmSync(tempRoot, { recursive: true, force: true })
  await rescan(request)

  if (deleteStatus !== null) {
    expect(deleteStatus, `DELETE /stacks/${row!.id} returned ${deleteStatus}`).toBeLessThan(300)
  }
  // A check, not a hope: the rescan prune would hide a refused delete.
  expect(findFixtureStack(await listStacks(request)), 'fixture stack row still listed after cleanup').toBeUndefined()
}

// ─── Suite ───────────────────────────────────────────────────────────────────

test.describe('git: commit and push from the UI', () => {
  test.beforeAll(async ({ request }) => {
    expect(
      AUTH_DISABLED,
      'git-commit-push.spec.ts needs a backend started with AUTH_DISABLED=true, and AUTH_DISABLED=true in this process',
    ).toBe(true)
    await ensureCsrf(request)

    const config = await request.get(`${API_URL}/api/v1/settings/config`)
    expect(config.status(), 'GET /settings/config').toBe(200)
    const { stacksDir } = (await config.json()) as { stacksDir: string }
    expect(stacksDir, 'stacksDir from /settings/config').toBeTruthy()

    // A leftover from an aborted run is ours (the name is reserved for this spec).
    stackFolder = join(stacksDir, STACK_NAME)
    await removeFixture(request)

    tempRoot = mkdtempSync(join(tmpdir(), 'capstan-git-commit-push-'))
    bareDir = join(tempRoot, 'remote.git')
    git(tempRoot, 'init', '--bare', '-b', 'main', bareDir)
    git(tempRoot, 'clone', bareDir, stackFolder)
    git(stackFolder, 'symbolic-ref', 'HEAD', 'refs/heads/main')
    writeFileSync(join(stackFolder, COMPOSE_FILE), SEED_COMPOSE)
    git(stackFolder, 'add', COMPOSE_FILE)
    git(stackFolder, 'commit', '-m', 'Seed compose')
    git(stackFolder, 'push', '-u', 'origin', 'main')

    await rescan(request)
    const row = findFixtureStack(await listStacks(request))
    expect(row, `stack '${STACK_NAME}' missing after the rescan`).toBeTruthy()
    stackId = row!.id

    const prior = await request.get(`${API_URL}/api/v1/settings/git`)
    if (prior.ok()) {
      const body = (await prior.json()) as { authorName?: string; authorEmail?: string }
      if (body.authorName && body.authorEmail) {
        priorAuthor = { name: body.authorName, email: body.authorEmail }
      }
    }

    const author = await request.put(`${API_URL}/api/v1/settings/git`, {
      headers: mutationHeaders(),
      data: { authorName: AUTHOR_NAME, authorEmail: AUTHOR_EMAIL },
    })
    expect(author.ok(), `PUT /settings/git returned ${author.status()}`).toBe(true)

    const push = await request.put(`${API_URL}/api/v1/directories/push`, {
      headers: mutationHeaders(),
      data: { path: row!.directory, enabled: true },
    })
    expect(push.ok(), `PUT /directories/push returned ${push.status()}`).toBe(true)
  })

  test.afterAll(async ({ request }) => {
    await ensureCsrf(request)
    await removeFixture(request)
    if (priorAuthor) {
      await request.put(`${API_URL}/api/v1/settings/git`, {
        headers: mutationHeaders(),
        data: { authorName: priorAuthor.name, authorEmail: priorAuthor.email },
      })
    } else {
      console.warn(
        `git-commit-push.spec.ts: the commit author was unset and cannot be cleared through the API; it is still "${AUTHOR_NAME} <${AUTHOR_EMAIL}>". Clear it in Settings > Git if this is not a throwaway DATA_DIR.`,
      )
    }
  })

  test('GIT-PW-001: edit compose, save, commit and push from the toast, commit lands on the remote', async ({
    page,
  }) => {
    const before = remote('rev-parse', 'main')

    await page.goto(`/stacks/${encodeURIComponent(stackId)}/editor`)
    const cm = page.locator('.cm-content').first()
    await expect(cm).toBeVisible()
    await expect(cm).toContainText('nginx:1.25-alpine')
    await cm.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type(`\n${EDIT_MARKER}`)
    await expect(cm).toContainText(EDIT_MARKER)

    // Saving only writes the file: the remote has not moved.
    await page.getByRole('button', { name: 'Save', exact: true }).click()
    const toast = page.locator('[data-sonner-toast]').filter({ hasText: 'Compose file saved' })
    await expect(toast).toBeVisible()
    expect(remote('rev-parse', 'main'), 'a save must not touch the remote').toBe(before)

    await toast.getByRole('button', { name: 'Commit and push' }).click()

    const dialog = page.getByRole('dialog', { name: 'Commit and push' })
    await expect(dialog).toBeVisible()
    // The compose file is listed and pre-ticked; unticking it would commit nothing.
    const composeBox = dialog.getByRole('checkbox', { name: new RegExp(COMPOSE_FILE) })
    await expect(composeBox).toBeChecked()

    await dialog.getByLabel('Commit message').fill(COMMIT_MESSAGE)
    await dialog.getByRole('button', { name: 'Commit and push', exact: true }).click()

    // "Commit <hash>: committed and pushed <branch> to <remote>" (CommitPushDialog outcomeOfResult).
    await expect(dialog.getByRole('status')).toContainText('committed and pushed')

    // Read the bare remote: message, author, and the file's content.
    await expect.poll(() => remote('log', '-1', '--format=%s', 'main')).toBe(COMMIT_MESSAGE)
    expect(remote('rev-parse', 'main')).not.toBe(before)
    expect(remote('log', '-1', '--format=%an <%ae>', 'main')).toBe(`${AUTHOR_NAME} <${AUTHOR_EMAIL}>`)
    expect(remote('show', '--name-only', '--format=', 'main')).toBe(COMPOSE_FILE)
    expect(remote('show', `main:${COMPOSE_FILE}`)).toContain(EDIT_MARKER)
  })
})
