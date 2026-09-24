// The fields both the Containers overview and the Updates tab carry for a
// compose project's stack association (DashboardContainerInfo,
// ContainerUpdateInfo, CachedUpdate).
export interface ComposeStackFields {
  projectName?: string
  stackId?: string
  stackLookupFailed: boolean
  composeWorkingDir?: string
  composeConfigFiles?: string
}

// agent-os-fnch. The no-row-no-error state: a compose project Capstan has no
// stack record for, AND the lookup succeeded. A failed lookup is excluded
// because "we could not tell" is not "unmanaged".
export function isUnmanagedCompose(c: ComposeStackFields): boolean {
  return Boolean(c.projectName) && !c.stackLookupFailed && !c.stackId
}
