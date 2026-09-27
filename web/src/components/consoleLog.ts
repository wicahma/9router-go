const ANSI_RE = /\u001b\[[0-9;]*m/g

/** Server lines can still carry terminal colors (e.g. captured stdout). */
export function stripAnsi(line: string): string {
  return line.replace(ANSI_RE, '')
}

/**
 * Case-insensitive substring filter over console log lines. ANSI codes are
 * stripped before matching so a query matches what the viewer actually shows.
 */
export function filterConsoleLogs(logs: string[], query: string): string[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return logs
  return logs.filter((line) => stripAnsi(line).toLowerCase().includes(needle))
}
