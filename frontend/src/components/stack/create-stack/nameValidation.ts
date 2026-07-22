export function validateName(value: string): string {
  if (!value.trim()) {
    return 'Stack name is required'
  }
  if (!/^[a-zA-Z0-9._-]+$/.test(value)) {
    return 'Only letters, numbers, dots, hyphens, and underscores are allowed'
  }
  if (value.length < 1 || value.length > 50) {
    return 'Stack name must be between 1 and 50 characters'
  }
  return ''
}
