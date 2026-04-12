// @ts-nocheck

import { http, HttpResponse, delay, setupServer } from 'msw'
import type { User } from '@/types'

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
          id: 'stack1:default',
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

  http.post('/api/v1/stacks/:id/start', async ({ params }) => {
    await delay(300)
    return HttpResponse.json({
      status: 'started',
      output: 'Container started',
      duration: 300,
    })
  }),

  http.post('/api/v1/stacks/:id/stop', async ({ params }) => {
    await delay(300)
    return HttpResponse.json({
      status: 'stopped',
      output: 'Container stopped',
      duration: 300,
    })
  }),

  http.post('/api/v1/stacks/:id/restart', async ({ params }) => {
    await delay(500)
    return HttpResponse.json({
      status: 'restarted',
      output: 'Container restarted',
      duration: 500,
    })
  }),

  http.get('/api/v1/stacks/:id/compose', async ({ params }) => {
    await delay(50)
    return HttpResponse.json({
      content: 'services:\n  web:\n    image: nginx:1.21\n',
      filename: 'compose.yaml',
      size: 50,
      lastModified: new Date().toISOString(),
    })
  }),

  http.get('/api/v1/stacks/:id/env', async ({ params }) => {
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
]

export const server = setupServer(...handlers)

