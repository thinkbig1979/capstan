// The one frontend copy of the server's secret-name rule, IsSensitiveEnvKey
// (backend/internal/services/redact_compose.go). Both are tested against the
// same vector (agent-os-yt2f), so change them together.
//
// The key is split at every character that is not a letter or digit, and
// trailing digits are dropped from each word. It is sensitive when a word is
// one of SENSITIVE_WORDS, ends in one of SENSITIVE_WORD_ENDINGS, or the key
// contains "_API_". KEY counts only as a whole word, so MONKEY stays visible.
const SENSITIVE_WORDS = new Set(['PASSWORD', 'PASSWD', 'SECRET', 'TOKEN', 'KEY', 'APIKEY', 'PRIVATEKEY'])
const SENSITIVE_WORD_ENDINGS = ['PASSWORD', 'PASSWD', 'SECRET', 'TOKEN']

export function isSensitiveKey(key: string) {
  const upper = key.toUpperCase()
  for (const word of upper.split(/[^A-Z0-9]+/)) {
    const w = word.replace(/[0-9]+$/, '')
    if (w === '') continue
    if (SENSITIVE_WORDS.has(w) || SENSITIVE_WORD_ENDINGS.some((end) => w.endsWith(end))) {
      return true
    }
  }
  return upper.includes('_API_')
}
