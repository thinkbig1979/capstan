export function inferVarName(yamlContent: string, cursorPos: number): string {
  const lines = yamlContent.split('\n')
  let charIdx = 0
  let currentLineIdx = 0

  for (let i = 0; i < lines.length; i++) {
    const lineEnd = charIdx + lines[i].length
    if (charIdx <= cursorPos && cursorPos <= lineEnd) {
      currentLineIdx = i
      break
    }
    charIdx = lineEnd + 1
  }

  const currentLine = lines[currentLineIdx]
  const colonMatch = currentLine.match(/^\s*([a-zA-Z0-9_-]+)\s*:/)
  if (colonMatch) {
    const key = colonMatch[1].replace(/-/g, '_').toUpperCase()
    return key
  }

  const listMatch = currentLine.match(/^\s*-\s*([A-Za-z0-9_]+)\s*=/)
  if (listMatch) {
    return listMatch[1].replace(/-/g, '_').toUpperCase()
  }

  for (let i = currentLineIdx - 1; i >= Math.max(0, currentLineIdx - 5); i--) {
    const line = lines[i]
    const keyMatch = line.match(/^\s*([a-zA-Z0-9_-]+)\s*:/)
    if (keyMatch && !keyMatch[1].match(/^(services|environment|ports|volumes|networks|depends_on|deploy|build|image|restart|container_name|hostname|labels|command|entrypoint|env_file|extra_hosts|healthcheck|logging|cap_add|cap_drop|devices|dns|tmpfs|ulimits|security_opt|shm_size|stdin_open|tty|user|working_dir|domainname|mac_address|privileged|read_only|pid|cgroup_parent|network_mode|stop_signal|stop_grace_period|isolation|configs|secrets|links|external_links|sysctls|named|anonymous)$/i)) {
      return keyMatch[1].replace(/-/g, '_').toUpperCase()
    }
  }

  return 'ENV_VAR'
}
