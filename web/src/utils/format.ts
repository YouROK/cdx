// Plain date and time formatting without external dependencies.

/** formatDateTime turns epoch seconds into a date and time. */
export function formatDateTime(seconds: number): string {
  if (!seconds) {
    return '-'
  }
  return new Date(seconds * 1000).toLocaleString()
}
