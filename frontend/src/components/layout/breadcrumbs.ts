import { hostViewLabel, isHostView } from '@/components/host/host-views'

export interface Breadcrumb {
  label: string
  path: string
}

export function getBreadcrumbs(path: string): Breadcrumb[] {
  if (path === '/') {
    return [{ label: 'Dashboard', path: '/' }]
  }
  if (path.startsWith('/stacks/')) {
    // Routes are /stacks/<id>/<tab>; the tab lives in the path, not the query string.
    const segments = path.split('/').filter(Boolean) // ['stacks', '<encId>', '<tab>?']
    const encId = segments[1] || ''
    const tab = segments[2] || 'overview'
    const tabLabel = tab.charAt(0).toUpperCase() + tab.slice(1)
    // Derive a friendly stack name from the id (format: <dir>~<name>:<profile>).
    let decodedId = encId
    try { decodedId = decodeURIComponent(encId) } catch { /* keep raw */ }
    const nameMatch = decodedId.match(/~([^~:]+)(?::[^:]*)?$/)
    const stackName = nameMatch ? nameMatch[1] : 'Stack Details'
    const stackPath = `/stacks/${encId}`
    return [
      { label: 'Dashboard', path: '/' },
      { label: stackName, path: stackPath },
      { label: tabLabel, path: `${stackPath}/${tab}` },
    ]
  }
  if (path === '/host' || path.startsWith('/host/')) {
    const crumbs = [
      { label: 'Dashboard', path: '/' },
      { label: 'Docker host', path: '/host' },
    ]
    const view = path.split('/')[2]
    if (isHostView(view)) crumbs.push({ label: hostViewLabel(view), path: `/host/${view}` })
    return crumbs
  }
  if (path === '/settings') {
    return [
      { label: 'Dashboard', path: '/' },
      { label: 'Settings', path: '/settings' },
    ]
  }
  return [{ label: 'Dashboard', path: '/' }]
}
