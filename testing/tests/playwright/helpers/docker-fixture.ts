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
/** A stopped container carrying a compose project label: never a Free up candidate. */
export const LABELLED_CONTAINER = 'capstan-e2e-labelled'
const COMPOSE_PROJECT_LABEL = 'com.docker.compose.project'

const DESTRUCTIVE_WHAT =
  'Removes EVERY unlabelled stopped container (and, with defaults, unused images and build cache) on this ' +
  'Docker host, not only the fixture, so it runs only on an ephemeral GitHub-hosted runner.'

/**
 * Why a test that removes what the Free up dialog lists must skip here, or
 * undefined when it may run. The opt-in alone is not enough: it also needs a
 * GitHub-hosted runner (an ephemeral VM), so a stray export on a developer
 * machine or the self-hosted runner removes nothing.
 * A local `act` run can set both GitHub variables too, so never run the
 * backup-flow job under act with CAPSTAN_E2E_DESTRUCTIVE=1 on your own machine.
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

export function containerId(name: string): string {
  return docker(['container', 'inspect', '--format', '{{.Id}}', name]).trim()
}

/**
 * Creates the exited, 1 MiB container named `name`. It writes 1 MiB because a
 * container that writes nothing has a SizeRw of 0 and frees nothing
 * (host_space.go).
 */
function createExitedContainer(name: string, labels: string[]): void {
  removeContainer(name)
  const labelArgs = labels.flatMap((l) => ['--label', l])
  docker(['run', '--name', name, ...labelArgs, FIXTURE_IMAGE, 'sh', '-c', 'head -c 1048576 /dev/zero > /leftover'])
}

function removeContainer(name: string): void {
  if (exists('container', name)) docker(['rm', '-f', name])
}

/** An exited container with no compose label, so the Host tab counts it as a leftover. */
export function createLeftoverContainer(): void {
  createExitedContainer(LEFTOVER_CONTAINER, [])
}

export function removeLeftoverContainer(): void {
  removeContainer(LEFTOVER_CONTAINER)
}

/**
 * The same exited container, but labelled with a compose project, as a
 * stopped stack's container is. The candidate rule never lists it
 * (containerIsCandidate in host_space.go), so Remove selected must leave it.
 */
export function createLabelledStoppedContainer(): void {
  createExitedContainer(LABELLED_CONTAINER, [`${COMPOSE_PROJECT_LABEL}=${LABELLED_CONTAINER}`])
}

export function removeLabelledStoppedContainer(): void {
  removeContainer(LABELLED_CONTAINER)
}

/** A volume no container mounts and no stack owns: an "Unattached volume". */
export function createLeftoverVolume(): void {
  if (!exists('volume', LEFTOVER_VOLUME)) docker(['volume', 'create', LEFTOVER_VOLUME])
}

export function removeLeftoverVolume(): void {
  if (exists('volume', LEFTOVER_VOLUME)) docker(['volume', 'rm', LEFTOVER_VOLUME])
}

interface HostCandidatesBody {
  containers: { id: string }[]
  volumes: { id: string }[]
}

/**
 * The server's candidate lists from a fresh Docker read. This also refreshes
 * the snapshot the host summary serves (Candidates in host_space.go), which is
 * otherwise up to 30 s old, so a page loaded after this sees the fixtures.
 */
export async function freshHostCandidates(apiUrl: string): Promise<HostCandidatesBody> {
  const resp = await fetch(`${apiUrl}/api/v1/resources/host/candidates`)
  if (!resp.ok) throw new Error(`GET /api/v1/resources/host/candidates returned ${resp.status}`)
  return (await resp.json()) as HostCandidatesBody
}

/**
 * Creates the leftover container and waits until the server lists it, so the
 * Free up button is enabled on the next page load whatever the snapshot held.
 */
export async function createListedLeftoverContainer(apiUrl: string): Promise<string> {
  createLeftoverContainer()
  const id = containerId(LEFTOVER_CONTAINER)
  const listed = (await freshHostCandidates(apiUrl)).containers.some((c) => c.id === id)
  if (!listed) throw new Error(`${LEFTOVER_CONTAINER} (${id}) is not among the server's container candidates`)
  return id
}
