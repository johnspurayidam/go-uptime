export type MonitorStatus = "up" | "down" | "unknown"

export type Monitor = {
  id: number
  name: string
  url: string
  interval_seconds: number
  created_at: string
  status: MonitorStatus
  last_latency_ms: number | null
  uptime_percent: number | null
}

export type Check = {
  id: number
  monitor_id: number
  status_code: number
  latency_ms: number
  success: boolean
  error?: string
  checked_at: string
}

export type IncidentStatus = "open" | "acked" | "resolved"

export type Incident = {
  id: number
  monitor_id: number
  status: IncidentStatus
  opened_at: string
  acked_at: string | null
  resolved_at: string | null
  last_error: string
  monitor_name?: string
}

export type NewMonitor = {
  name: string
  url: string
  interval_seconds?: number
}
