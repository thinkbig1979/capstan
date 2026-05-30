import { http, HttpResponse, delay, setupServer } from 'msw'

export const handlers = [
  http.get('/api/v1/auth/status', async () => {
    await delay(100)
    return HttpResponse.json({
      needsSetup: false,
      authDisabled: false,
    })
  }),

  http.post('/api/v1/auth/setup', async ({ request }) => {
    await delay(100)
    const body = await request.json() as { username: string; password: string }

    if (!body.username || !body.password) {
      return HttpResponse.json(
        { error: 'Validation failed', code: 'VALIDATION_ERROR' },
        { status: 400 }
      )
    }

    return HttpResponse.json({
      token: 'mock-jwt-token',
      user: {
        id: 'user-1',
        username: body.username,
      },
    })
  }),

  http.post('/api/v1/auth/login', async ({ request }) => {
    await delay(100)
    const body = await request.json() as { username: string; password: string }

    if (body.username === 'validuser' && body.password === 'validpassword') {
      return HttpResponse.json({
        token: 'mock-jwt-token',
        user: {
          id: 'user-1',
          username: 'validuser',
        },
      })
    }

    return HttpResponse.json(
      { error: 'Invalid credentials', code: 'UNAUTHORIZED' },
      { status: 401 }
    )
  }),

  http.post('/api/v1/auth/logout', async () => {
    await delay(50)
    return new HttpResponse(null, { status: 204 })
  }),

  http.get('/api/v1/auth/me', async ({ request }) => {
    await delay(50)
    const authHeader = request.headers.get('Authorization')

    if (!authHeader) {
      return HttpResponse.json(
        { error: 'Unauthorized', code: 'UNAUTHORIZED' },
        { status: 401 }
      )
    }

    return HttpResponse.json({
      id: 'user-1',
      username: 'validuser',
    })
  }),

  http.get('/api/v1/directories', async () => {
    await delay(100)
    return HttpResponse.json({
      directories: [
        {
          path: '/opt/stacks/stack1',
          name: 'stack1',
          isGitRepo: false,
          gitRemote: '',
          gitBranch: '',
          scannedAt: new Date().toISOString(),
          stackCount: 1,
        },
      ],
    })
  }),

  http.post('/api/v1/directories/scan', async () => {
    await delay(500)
    return HttpResponse.json({
      directories: [],
      hasGlobalEnv: false,
      scannedAt: new Date().toISOString(),
    })
  }),

  http.get('/api/v1/stacks', async () => {
    await delay(100)
    return HttpResponse.json({
      stacks: [
        {
          id: 'stacks~stack1:default',
          directory: '/opt/stacks/stack1',
          composeFile: 'compose.yaml',
          envFile: '.env',
          projectName: 'stack1-default',
          status: 'running',
          isGitRepo: false,
          gitBranch: '',
          containers: [],
        },
      ],
    })
  }),

  http.get('/api/v1/stacks/:id', async ({ params }) => {
    await delay(50)
    return HttpResponse.json({
      id: params.id,
      directory: '/opt/stacks/stack1',
      composeFile: 'compose.yaml',
      envFile: '.env',
      projectName: 'stack1-default',
      status: 'running',
      isGitRepo: false,
      gitBranch: '',
      containers: [],
    })
  }),

  http.post('/api/v1/stacks/:id/start', async () => {
    await delay(300)
    return HttpResponse.json({
      status: 'started',
      output: 'Container started',
      duration: 300,
    })
  }),

  http.post('/api/v1/stacks/:id/stop', async () => {
    await delay(300)
    return HttpResponse.json({
      status: 'stopped',
      output: 'Container stopped',
      duration: 300,
    })
  }),

  http.post('/api/v1/stacks/:id/restart', async () => {
    await delay(500)
    return HttpResponse.json({
      status: 'restarted',
      output: 'Container restarted',
      duration: 500,
    })
  }),

  http.get('/api/v1/stacks/:id/compose', async () => {
    await delay(50)
    return HttpResponse.json({
      content: 'services:\n  web:\n    image: nginx:1.21\n',
      filename: 'compose.yaml',
      size: 50,
      lastModified: new Date().toISOString(),
    })
  }),

  http.get('/api/v1/stacks/:id/env', async () => {
    await delay(50)
    return HttpResponse.json({
      filename: '.env',
      entries: [
        { key: 'PORT', value: '8080', line: 1, sensitive: false, comment: false },
        { key: 'API_KEY', value: 'secret', line: 2, sensitive: true, comment: false },
      ],
      raw: 'PORT=8080\nAPI_KEY=secret\n',
    })
  }),

  http.post('/api/v1/compose/lint', async () => {
    await delay(100)
    return HttpResponse.json({
      valid: true,
      lintResults: [],
    })
  }),

  http.post('/api/v1/stacks/:id/compose/lint', async () => {
    await delay(100)
    return HttpResponse.json({
      valid: true,
      lintResults: [],
    })
  }),

  // ────────────────────────────────────────────────
  // Backup — Settings
  // ────────────────────────────────────────────────

  http.get('/api/v1/settings/backup', async () => {
    await delay(100)
    return HttpResponse.json({
      repository: '/app/data/restic-repo',
      repositorySource: 'default',
      hasPassword: false,
      passwordSource: 'default',
      keepDaily: 7,
      keepWeekly: 4,
      keepMonthly: 6,
      keepYearly: 0,
      autoPrune: true,
      scheduleIntervalMinutes: 0,
      syncAfterBackup: false,
      rcloneRemote: '',
      rclonePath: '',
      rcloneTransfers: 4,
      hostname: 'mock-host',
      resticAvailable: true,
      rcloneAvailable: true,
      repositoryInitialized: false,
    })
  }),

  http.put('/api/v1/settings/backup', async ({ request }) => {
    await delay(100)
    const body = await request.json()
    return HttpResponse.json({
      repository: '/app/data/restic-repo',
      repositorySource: 'db',
      hasPassword: false,
      passwordSource: 'default',
      keepDaily: 7,
      keepWeekly: 4,
      keepMonthly: 6,
      keepYearly: 0,
      autoPrune: true,
      scheduleIntervalMinutes: 0,
      syncAfterBackup: false,
      rcloneRemote: '',
      rclonePath: '',
      rcloneTransfers: 4,
      hostname: 'mock-host',
      resticAvailable: true,
      rcloneAvailable: true,
      repositoryInitialized: false,
      ...(body as object),
    })
  }),

  // ────────────────────────────────────────────────
  // Backup — Policies
  // ────────────────────────────────────────────────

  http.get('/api/v1/backups/policies', async () => {
    await delay(100)
    return HttpResponse.json({
      policies: [
        {
          id: 'policy-1',
          targetType: 'stack',
          targetId: 'stacks~myapp',
          enabled: true,
          stopPolicy: 'stop',
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
        },
      ],
    })
  }),

  http.put('/api/v1/backups/policies/stack/:stackId', async ({ params, request }) => {
    await delay(100)
    const body = await request.json() as { enabled: boolean; stopPolicy?: string }
    return HttpResponse.json({
      id: 'policy-1',
      targetType: 'stack',
      targetId: decodeURIComponent(params.stackId as string),
      enabled: body.enabled ?? true,
      stopPolicy: body.stopPolicy ?? 'stop',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    })
  }),

  http.delete('/api/v1/backups/policies/stack/:stackId', async () => {
    await delay(100)
    return new HttpResponse(null, { status: 204 })
  }),

  // ────────────────────────────────────────────────
  // Backup — Status & History
  // ────────────────────────────────────────────────

  http.get('/api/v1/backups/status', async () => {
    await delay(100)
    return HttpResponse.json({
      resticAvailable: true,
      rcloneAvailable: true,
      repositoryInitialized: false,
      enabledStackCount: 1,
      lastRun: null,
      nextRunAt: null,
      repoSizeBytes: null,
      schedulerRunning: false,
    })
  }),

  http.get('/api/v1/backups/history', async () => {
    await delay(100)
    return HttpResponse.json({
      runs: [
        {
          id: 'run-1',
          kind: 'backup',
          trigger: 'manual',
          status: 'success',
          startedAt: new Date(Date.now() - 3600000).toISOString(),
          finishedAt: new Date(Date.now() - 3540000).toISOString(),
          stacksTotal: 2,
          stacksOk: 2,
          stacksFailed: 0,
          bytesAdded: 1048576,
          errorMessage: '',
        },
      ],
    })
  }),

  http.get('/api/v1/backups/runs/:runId', async ({ params }) => {
    await delay(50)
    return HttpResponse.json({
      run: {
        id: params.runId,
        kind: 'backup',
        trigger: 'manual',
        status: 'success',
        startedAt: new Date(Date.now() - 3600000).toISOString(),
        finishedAt: new Date(Date.now() - 3540000).toISOString(),
        stacksTotal: 1,
        stacksOk: 1,
        stacksFailed: 0,
        bytesAdded: 524288,
        errorMessage: '',
      },
      items: [
        {
          id: 'item-1',
          runId: params.runId,
          stackId: 'stacks~myapp',
          status: 'success',
          snapshotId: 'abc12345',
          stopApplied: true,
          durationMs: 12000,
          errorMessage: '',
        },
      ],
    })
  }),

  // ────────────────────────────────────────────────
  // Backup — Snapshots
  // ────────────────────────────────────────────────

  http.get('/api/v1/backups/snapshots', async () => {
    await delay(150)
    return HttpResponse.json([
      {
        id: 'abcdef1234567890abcdef1234567890abcdef12',
        shortId: 'abc12345',
        time: new Date(Date.now() - 3600000).toISOString(),
        hostname: 'mock-host',
        tags: ['stack:myapp'],
        paths: ['/stacks/myapp'],
        sizeBytes: 1048576,
      },
    ])
  }),

  http.get('/api/v1/backups/snapshots/:snapshotId/preview', async () => {
    await delay(200)
    return HttpResponse.json({
      entries: [
        '/stacks/myapp/compose.yaml',
        '/stacks/myapp/.env',
        '/stacks/myapp/data/db.sqlite',
      ],
    })
  }),

  // ────────────────────────────────────────────────
  // Backup — Operations
  // ────────────────────────────────────────────────

  http.post('/api/v1/backups/run', async () => {
    await delay(100)
    return HttpResponse.json({
      runId: 'run-mock-1',
      wsUrl: '/ws/backups/run/run-mock-1',
    })
  }),

  http.post('/api/v1/backups/sync', async () => {
    await delay(100)
    return HttpResponse.json({
      runId: 'run-mock-2',
      wsUrl: '/ws/backups/sync/run-mock-2',
    })
  }),

  http.post('/api/v1/backups/restore', async () => {
    await delay(100)
    return HttpResponse.json({
      runId: 'run-mock-3',
      wsUrl: '/ws/backups/restore/run-mock-3',
    })
  }),

  http.post('/api/v1/backups/dr-restore', async () => {
    await delay(100)
    return HttpResponse.json({
      runId: 'run-mock-4',
      wsUrl: '/ws/backups/dr-restore/run-mock-4',
    })
  }),

  http.post('/api/v1/backups/repo/init', async () => {
    await delay(300)
    return HttpResponse.json({ initialized: true })
  }),

  http.post('/api/v1/backups/cloud/test', async () => {
    await delay(500)
    return HttpResponse.json({ ok: true })
  }),

  http.post('/api/v1/backups/prune', async () => {
    await delay(100)
    return HttpResponse.json({
      runId: 'run-mock-5',
      wsUrl: '/ws/backups/prune/run-mock-5',
    })
  }),
]

export const server = setupServer(...handlers)
