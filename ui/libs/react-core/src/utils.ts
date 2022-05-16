export function getKey(name: string, disabled?: boolean) {
  if (disabled) {
    return null
  }

  return `sia/${name}`
}
