import { useCallback, useEffect, useRef, useState } from "react"
import { ApiError, ackIncident, createMonitor, deleteMonitor, eventsURL, listChecks, listIncidents, listMonitors, resolveIncident } from "./api.ts"
import { AddMonitorForm } from "./components/AddMonitorForm.tsx"
import { Header } from "./components/Header.tsx"
import { IncidentPanel } from "./components/IncidentPanel.tsx"
import { LatencyChart } from "./components/LatencyChart.tsx"
import { MonitorList } from "./components/MonitorList.tsx"
import type { Check, Incident, Monitor, NewMonitor } from "./types.ts"

const reconnecting = "Reconnecting to live updates"

// App owns the server data. A server-sent event stream reloads it when a probe or incident changes,
// and again right after a create, delete, ack, or resolve.
export default function App() {
  const [monitors, setMonitors] = useState<Monitor[]>([])
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [checks, setChecks] = useState<Check[]>([])
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [updatedAt, setUpdatedAt] = useState<string | null>(null)
  const [ready, setReady] = useState(false)
  const requestSeq = useRef(0)

  // refresh reloads monitors, incidents, and the selected monitor's checks.
  // A newer refresh discards an older response that finishes late.
  const refresh = useCallback(async () => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    try {
      const [nextMonitors, nextIncidents] = await Promise.all([
        listMonitors(),
        listIncidents(),
      ])
      let nextSelected = selectedId
      if (nextSelected != null && !nextMonitors.some((monitor) => monitor.id === nextSelected)) {
        nextSelected = null
      }
      let nextChecks: Check[] = []
      if (nextSelected != null) {
        try {
          nextChecks = await listChecks(nextSelected)
        } catch (err) {
          if (!(err instanceof ApiError) || err.status !== 404) {
            throw err
          }
          nextSelected = null
        }
      }
      if (seq !== requestSeq.current) {
        return
      }
      setMonitors(nextMonitors)
      setIncidents(nextIncidents)
      setChecks(nextChecks)
      setSelectedId(nextSelected)
      setLoadError(null)
      setUpdatedAt(
        new Date().toLocaleTimeString(undefined, {
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
        }),
      )
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : "Could not reach the API")
    } finally {
      setReady(true)
    }
  }, [selectedId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const refreshRef = useRef(refresh)
  refreshRef.current = refresh

  // One EventSource for the life of the page. Named events reload the lists.
  // onerror shows the reconnect banner. The browser reconnects by itself.
  // closed skips setState after this effect cleans up.
  useEffect(() => {
    const source = new EventSource(eventsURL())
    let closed = false
    source.onopen = () => {
      if (closed) {
        return
      }
      setLoadError((current) => (current === reconnecting ? null : current))
      void refreshRef.current()
    }
    source.onerror = () => {
      if (!closed) {
        setLoadError(reconnecting)
      }
    }
    const onLiveUpdate = () => {
      if (!closed) {
        void refreshRef.current()
      }
    }
    source.addEventListener("check_result", onLiveUpdate)
    source.addEventListener("incident_opened", onLiveUpdate)
    source.addEventListener("incident_updated", onLiveUpdate)
    return () => {
      closed = true
      source.close()
    }
  }, [])

  async function handleCreate(input: NewMonitor) {
    setFormError(null)
    try {
      await createMonitor(input)
      await refresh()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : "Could not add the monitor")
      throw err
    }
  }

  async function handleDelete(monitor: Monitor) {
    if (!window.confirm(`Delete ${monitor.name}?`)) {
      return
    }
    try {
      await deleteMonitor(monitor.id)
      if (selectedId === monitor.id) {
        setSelectedId(null)
        setChecks([])
      }
      await refresh()
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : "Could not delete the monitor")
    }
  }

  async function handleAck(id: number) {
    try {
      await ackIncident(id)
      await refresh()
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : "Could not acknowledge the incident")
    }
  }

  async function handleResolve(id: number) {
    try {
      await resolveIncident(id)
      await refresh()
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : "Could not resolve the incident")
    }
  }

  const selected = monitors.find((monitor) => monitor.id === selectedId) ?? null
  const open = incidents.filter((incident) => incident.status === "open")
  const acked = incidents.filter((incident) => incident.status === "acked")
  const resolved = incidents.filter((incident) => incident.status === "resolved")

  return (
    <>
      <Header monitors={monitors} updatedAt={updatedAt} />
      <main className="page">
        {loadError ? <p className="banner">{loadError}</p> : null}
        {!ready ? (
          <p className="empty">Loading…</p>
        ) : (
        <div className="layout">
          <div className="column">
            <AddMonitorForm
              onSubmit={handleCreate}
              error={formError}
            />
            <section className="card">
              <h2>Monitors</h2>
              <MonitorList
                monitors={monitors}
                selectedId={selectedId}
                onSelect={setSelectedId}
                onDelete={(monitor) => void handleDelete(monitor)}
              />
            </section>
            <LatencyChart monitorName={selected?.name ?? null} checks={checks} />
          </div>
          <IncidentPanel
            open={open}
            acked={acked}
            resolved={resolved}
            onAck={(id) => void handleAck(id)}
            onResolve={(id) => void handleResolve(id)}
          />
        </div>
        )}
      </main>
    </>
  )
}
