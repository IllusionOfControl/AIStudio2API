import type { AdminLog } from './types'

// LogRow stores the latest state and full event timeline of a request
export interface LogRow {
  key: string
  entry: AdminLog
  events: AdminLog[]
}

// groupLogs merges lifecycles by request ID while preserving original order of service events
export function groupLogs(logs: AdminLog[]): LogRow[] {
  const rows: LogRow[] = []
  const requests = new Map<string, LogRow>()
  for (const [index, entry] of logs.entries()) {
    const id = entry.request?.id
    const row = id ? requests.get(id) : undefined
    if (row) {
      row.events.push(entry)
      row.entry = { ...entry, request: { ...row.entry.request!, ...entry.request! } }
    } else {
      const next = { key: id || `${entry.time}:${index}`, entry, events: [entry] }
      rows.push(next)
      if (id) requests.set(id, next)
    }
  }
  return rows
}
