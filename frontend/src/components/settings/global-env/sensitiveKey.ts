const SENSITIVE_PATTERNS = ['_KEY', '_SECRET', '_PASSWORD', '_TOKEN', '_API_']

export function isSensitiveKey(key: string) {
  const upper = key.toUpperCase()
  return SENSITIVE_PATTERNS.some((p) => upper.includes(p))
}
