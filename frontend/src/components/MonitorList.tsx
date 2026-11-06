import { formatLatency, formatUptime } from "../format.ts"
import type { Monitor } from "../types.ts"

type MonitorListProps = {
  monitors: Monitor[]
  selectedId: number | null
  onSelect: (id: number) => void
  onDelete: (monitor: Monitor) => void
}

export function MonitorList({ monitors, selectedId, onSelect, onDelete }: MonitorListProps) {
  if (monitors.length === 0) {
    return <p className="empty">No monitors yet. Add one above.</p>
  }

  return (
    <ul className="monitor-list">
      {monitors.map((monitor) => {
        const selected = monitor.id === selectedId
        return (
          <li key={monitor.id} className={selected ? "monitor selected" : "monitor"}>
            <button
              type="button"
              className="monitor-main"
              onClick={() => onSelect(monitor.id)}
              aria-pressed={selected}
            >
              <span className={`status-dot ${monitor.status}`} title={monitor.status} />
              <span className="monitor-name">
                {monitor.name}
                <span className="monitor-url">{monitor.url}</span>
              </span>
              <span className="metric">
                <span className="metric-label">Latency</span>
                {formatLatency(monitor.last_latency_ms)}
              </span>
              <span className="metric">
                <span className="metric-label">24h uptime</span>
                {formatUptime(monitor.uptime_percent)}
              </span>
            </button>
            <button
              type="button"
              className="button ghost"
              onClick={() => onDelete(monitor)}
            >
              Delete
            </button>
          </li>
        )
      })}
    </ul>
  )
}
