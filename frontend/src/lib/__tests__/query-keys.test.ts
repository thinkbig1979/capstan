import { describe, it, expect } from 'vitest'
import { queryKeys } from '../query-keys'

/**
 * These tests pin the literal wire shape of every cache key.
 *
 * Call sites go through the factory, so a typo there can no longer desync a
 * reader from a writer — but it could still silently rename a key and quietly
 * drop whatever the old key had cached. This file is the one place that asserts
 * the raw arrays, so any change to a key shape has to be made deliberately here.
 */
describe('queryKeys literal shapes', () => {
  it('pins the top-level keys', () => {
    expect(queryKeys.stacks()).toEqual(['stacks'])
    expect(queryKeys.dashboardStats()).toEqual(['dashboard-stats'])
    expect(queryKeys.config()).toEqual(['config'])
    expect(queryKeys.directories()).toEqual(['directories'])
    expect(queryKeys.scanDepth()).toEqual(['scan-depth'])
    expect(queryKeys.autoUpdatePolicies()).toEqual(['auto-update-policies'])
  })

  it('pins the stack family', () => {
    expect(queryKeys.stack.all()).toEqual(['stack'])
    expect(queryKeys.stack.detail('s1')).toEqual(['stack', 's1'])
    expect(queryKeys.stack.compose('s1')).toEqual(['stack', 's1', 'compose'])
    expect(queryKeys.stack.env('s1')).toEqual(['stack', 's1', 'env'])
  })

  it('pins the resources family', () => {
    expect(queryKeys.resources.all()).toEqual(['resources'])
    expect(queryKeys.resources.images()).toEqual(['resources', 'images'])
    expect(queryKeys.resources.volumes()).toEqual(['resources', 'volumes'])
    expect(queryKeys.resources.networks()).toEqual(['resources', 'networks'])
    expect(queryKeys.resources.buildCache()).toEqual(['resources', 'build-cache'])
    expect(queryKeys.resources.updates()).toEqual(['resources', 'updates'])
    expect(queryKeys.resources.updateJobs()).toEqual(['resources', 'update-jobs'])
  })

  it('pins the settings family', () => {
    expect(queryKeys.settings.all()).toEqual(['settings'])
    expect(queryKeys.settings.updates()).toEqual(['settings', 'updates'])
    expect(queryKeys.settings.git()).toEqual(['settings', 'git'])
    expect(queryKeys.settings.globalEnv()).toEqual(['settings', 'global-env'])
  })

  it('pins the git family', () => {
    expect(queryKeys.git.all('s1')).toEqual(['git', 's1'])
    expect(queryKeys.git.log('s1', 50, 0, 'a.yml')).toEqual(['git', 's1', 'log', 50, 0, 'a.yml'])
    expect(queryKeys.git.diff('s1', 'abc')).toEqual(['git', 's1', 'diff', 'abc'])
  })

  it('pins the backup family', () => {
    expect(queryKeys.backup.all()).toEqual(['backup'])
    expect(queryKeys.backup.settings()).toEqual(['backup', 'settings'])
    expect(queryKeys.backup.policies()).toEqual(['backup', 'policies'])
    expect(queryKeys.backup.status()).toEqual(['backup', 'status'])
    expect(queryKeys.backup.history(5)).toEqual(['backup', 'history', { limit: 5 }])
    expect(queryKeys.backup.snapshots('s1')).toEqual(['backup', 'snapshots', 's1'])
    expect(queryKeys.backup.run('r1')).toEqual(['backup', 'runs', 'r1'])
    expect(queryKeys.backup.snapshotPreview('snap1')).toEqual(['backup', 'snapshot-preview', 'snap1'])
    expect(queryKeys.backup.stackHistory(5, 's1')).toEqual([
      'backup', 'history', { limit: 5 }, 'stack', 's1',
    ])
  })

  it('pins the update-history and audit-log families', () => {
    expect(queryKeys.updateHistory.all()).toEqual(['update-history'])
    expect(queryKeys.updateHistory.list({ stackId: 's1' })).toEqual([
      'update-history', { stackId: 's1' },
    ])
    expect(
      queryKeys.auditLog({
        page: 2,
        pageSize: 50,
        action: 'login',
        search: 'q',
        dateFrom: 'a',
        dateTo: 'b',
      }),
    ).toEqual(['audit-log', 2, 50, 'login', 'q', 'a', 'b'])
  })
})

/**
 * react-query invalidates by key prefix, so the broad `all()` entries only reach
 * their narrower siblings if they are genuine array prefixes of them. These
 * assertions are what make `stack.all()` a safe "refresh every stack" call.
 */
describe('queryKeys prefix relationships', () => {
  const isPrefixOf = (prefix: readonly unknown[], key: readonly unknown[]) =>
    prefix.every((segment, i) => segment === key[i])

  it('stack.all() is a prefix of every stack-scoped key', () => {
    for (const key of [
      queryKeys.stack.detail('s1'),
      queryKeys.stack.compose('s1'),
      queryKeys.stack.env('s1'),
    ]) {
      expect(isPrefixOf(queryKeys.stack.all(), key)).toBe(true)
    }
  })

  it('resources.all() is a prefix of every resource kind', () => {
    for (const key of [
      queryKeys.resources.images(),
      queryKeys.resources.volumes(),
      queryKeys.resources.networks(),
      queryKeys.resources.buildCache(),
      queryKeys.resources.updates(),
      queryKeys.resources.updateJobs(),
    ]) {
      expect(isPrefixOf(queryKeys.resources.all(), key)).toBe(true)
    }
  })

  it('settings.all() is a prefix of every settings pane', () => {
    for (const key of [
      queryKeys.settings.updates(),
      queryKeys.settings.git(),
      queryKeys.settings.globalEnv(),
    ]) {
      expect(isPrefixOf(queryKeys.settings.all(), key)).toBe(true)
    }
  })

  it('backup.all() is a prefix of every backup key', () => {
    for (const key of [
      queryKeys.backup.settings(),
      queryKeys.backup.policies(),
      queryKeys.backup.status(),
      queryKeys.backup.history(),
      queryKeys.backup.snapshots('s1'),
      queryKeys.backup.run('r1'),
      queryKeys.backup.snapshotPreview('snap1'),
      queryKeys.backup.stackHistory(5, 's1'),
    ]) {
      expect(isPrefixOf(queryKeys.backup.all(), key)).toBe(true)
    }
  })

  it('git.all(stackId) is a prefix of that stack log and diff keys', () => {
    expect(isPrefixOf(queryKeys.git.all('s1'), queryKeys.git.log('s1'))).toBe(true)
    expect(isPrefixOf(queryKeys.git.all('s1'), queryKeys.git.diff('s1', 'abc'))).toBe(true)
    // ...but not of a different stack's keys, or invalidating one stack's git
    // state would blow away every other stack's.
    expect(isPrefixOf(queryKeys.git.all('s1'), queryKeys.git.diff('s2', 'abc'))).toBe(false)
  })

  it('updateHistory.all() is a prefix of any filtered list', () => {
    expect(
      isPrefixOf(queryKeys.updateHistory.all(), queryKeys.updateHistory.list({ stackId: 's1' })),
    ).toBe(true)
  })

  it('backup.history() is a prefix of stackHistory for the same limit', () => {
    // stackHistory nests under history, so history(limit) must not be a prefix
    // by object identity — react-query hashes structurally, so an equal {limit}
    // object is what makes this match.
    const history = queryKeys.backup.history(5)
    const stackHistory = queryKeys.backup.stackHistory(5, 's1')
    expect(JSON.stringify(stackHistory).startsWith(JSON.stringify(history).slice(0, -1))).toBe(true)
  })
})
