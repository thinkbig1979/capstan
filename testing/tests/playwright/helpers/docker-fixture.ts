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

const DESTRUCTIVE_WHAT =
  'Removes EVERY unlabelled stopped container (and, with defaults, unused images and build cache) on this ' +
  'Docker host, not only the fixture, so it runs only on an ephemeral GitHub-hosted runner.'

/**
 * Why a test that removes what the Free up dialog lists must skip here, or
 * undefined when it may run. The opt-in alone is not enough: it also needs a
 * GitHub-hosted runner (an ephemeral VM), so a stray export on a developer
 * machine or the self-hosted runner removes nothing.
 */
export function destructiveSkipReason(env: NodeJS.ProcessEnv = process.env): string | undefined {
  if (env.CAPSTAN_E2E_DESTRUCTIVE !== '1') return `${DESTRUCTIVE_WHAT} CAPSTAN_E2E_DESTRUCTIVE is not 1.`
  if (env.GITHUB_ACTIONS !== 'true') return `${DESTRUCTIVE_WHAT} GITHUB_ACTIONS is not true.`
  if (env.RUNNER_ENVIRONMENT !== 'github-hosted') {
    return `${DESTRUCTIVE_WHAT} RUNNER_ENVIRONMENT is ${env.RUNNER_ENVIRONMENT ?? 'unset'}, not github-hosted.`
  }
  return undefined
}

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
