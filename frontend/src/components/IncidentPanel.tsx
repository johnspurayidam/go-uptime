import { formatTimestamp } from "../format.ts"
import type { Incident } from "../types.ts"

type IncidentPanelProps = {
  open: Incident[]
  acked: Incident[]
  resolved: Incident[]
  onAck: (id: number) => void
  onResolve: (id: number) => void
}

export function IncidentPanel({ open, acked, resolved, onAck, onResolve }: IncidentPanelProps) {
  const active = [...open, ...acked]
  const recentResolved = resolved.slice(0, 10)

  return (
    <section className="card">
      <h2>Incidents</h2>
      {active.length === 0 ? (
        <p className="empty">No open incidents.</p>
      ) : (
        <ul className="incident-list">
          {active.map((incident) => (
            <li key={incident.id} className="incident">
              <div className="incident-head">
                <strong>{incident.monitor_name ?? `Monitor ${incident.monitor_id}`}</strong>
                <span className={`tag tag-${incident.status}`}>{incident.status}</span>
              </div>
              <p className="incident-meta">Opened {formatTimestamp(incident.opened_at)}</p>
              {incident.last_error ? <p className="incident-error">{incident.last_error}</p> : null}
              <div className="incident-actions">
                {incident.status === "open" ? (
                  <button type="button" className="button" onClick={() => onAck(incident.id)}>
                    Ack
                  </button>
                ) : null}
                <button
                  type="button"
                  className="button primary"
                  onClick={() => onResolve(incident.id)}
                >
                  Resolve
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      <details className="resolved">
        <summary>Recently resolved ({recentResolved.length})</summary>
        {recentResolved.length === 0 ? (
          <p className="empty">None yet.</p>
        ) : (
          <ul className="resolved-list">
            {recentResolved.map((incident) => (
              <li key={incident.id}>
                <span>{incident.monitor_name ?? `Monitor ${incident.monitor_id}`}</span>
                <span>
                  {incident.resolved_at
                    ? formatTimestamp(incident.resolved_at)
                    : formatTimestamp(incident.opened_at)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </details>
    </section>
  )
}
