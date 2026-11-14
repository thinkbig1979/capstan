/**
 * Named Docker fixtures for the Host tab specs (agent-os-bugc).
 *
 * Every fixture is created and removed by its exact name, never by a filter or
 * a prune: these specs also run on developer machines with real containers.
 * The image is one the CI job already pre-pulls for the test-app stack
 * (.github/workflows/e2e-backup.yml), so no fixture pulls from Docker Hub.
 */
import { execFileSync } from 'node:child_process'

export const FIXTURE_IMAGE =
  process.env.CAPSTAN_E2E_FIXTURE_IMAGE ?? 'public.ecr.aws/docker/library/nginx:1.25-alpine'
export const LEFTOVER_CONTAINER = 'capstan-e2e-leftover'
export const LEFTOVER_VOLUME = 'capstan-e2e-leftover-vol'

/** Opt-in for tests that remove what the Free up dialog lists (every unlabelled leftover on the host). */
export const DESTRUCTIVE = process.env.CAPSTAN_E2E_DESTRUCTIVE === '1'
export const DESTRUCTIVE_SKIP_REASON =
  'Removes EVERY unlabelled stopped container (and, with defaults, unused images and build cache) on this ' +
  'Docker host, not only the fixture. Set CAPSTAN_E2E_DESTRUCTIVE=1 only on an ephemeral runner ' +
  '(the GitHub-hosted backup-flow job does).'

function docker(args: string[]): string {
  return execFileSync('docker', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
}

/** True when `docker <kind> inspect <name>` finds it. */
export function exists(kind: 'container' | 'volume', name: string): boolean {
  try {
    docker([kind, 'inspect', name])
    return true
  } catch {
    return false
  }
}

/**
 * An exited container with no compose label, so the Host tab counts it as a
 * leftover. It writes 1 MiB, because a container that writes nothing has a
 * SizeRw of 0 and frees nothing (host_space.go).
 */
export function createLeftoverContainer(): void {
  removeLeftoverContainer()
  docker(['run', '--name', LEFTOVER_CONTAINER, FIXTURE_IMAGE, 'sh', '-c', 'head -c 1048576 /dev/zero > /leftover'])
}

export function removeLeftoverContainer(): void {
  if (exists('container', LEFTOVER_CONTAINER)) docker(['rm', '-f', LEFTOVER_CONTAINER])
}

/** A volume no container mounts and no stack owns: an "Unattached volume". */
export function createLeftoverVolume(): void {
  if (!exists('volume', LEFTOVER_VOLUME)) docker(['volume', 'create', LEFTOVER_VOLUME])
}

export function removeLeftoverVolume(): void {
  if (exists('volume', LEFTOVER_VOLUME)) docker(['volume', 'rm', LEFTOVER_VOLUME])
}
