import composerize from 'composerize'

export function isDockerRunCommand(input: string): boolean {
  const trimmed = input.trim()
  return /^\*?\s*docker\s+run\s/i.test(trimmed)
}

export function convertDockerRun(input: string): string {
  const trimmed = input.trim()

  const joined = trimmed
    .split(/\n/)
    .map((line) => {
      const r = line.replace(/\s*\\$/, '')
      return r
    })
    .join(' ')
    .replace(/\s+/g, ' ')
    .trim()

  const result = composerize(joined, null, 'latest')
  return wrapComments(result, 80)
}

function wrapComments(yaml: string, maxWidth: number): string {
  return yaml
    .split('\n')
    .flatMap((line) => {
      if (!line.startsWith('#') || line.length <= maxWidth) return [line]
      const words = line.replace(/^#\s*/, '').split(/\s+/)
      const lines: string[] = []
      let current = '#'
      for (const word of words) {
        if (current.length + 1 + word.length > maxWidth && current !== '#') {
          lines.push(current)
          current = `# ${word}`
        } else {
          current += ` ${word}`
        }
      }
      if (current !== '#') lines.push(current)
      return lines
    })
    .join('\n')
}
