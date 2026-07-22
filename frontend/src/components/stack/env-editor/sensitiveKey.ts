const SENSITIVE_KEY_PATTERNS = ['_KEY', '_SECRET', '_PASSWORD', '_TOKEN', '_API_']

export function isSensitiveKey(key: string) {
  return SENSITIVE_KEY_PATTERNS.some((pattern) => key.toUpperCase().includes(pattern))
}
