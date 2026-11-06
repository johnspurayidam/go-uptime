import type { Check, Incident, IncidentStatus, Monitor, NewMonitor } from "./types.ts"

// ApiError is a failed HTTP response. message is the API's JSON error field when it sent one.
export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

function apiBaseURL(): string {
  const configured = import.meta.env.VITE_API_BASE_URL
  if (typeof configured === "string" && configured.trim() !== "") {
    return configured.replace(/\/$/, "")
  }
  return "http://localhost:8080"
}

// eventsURL is the server-sent event stream. The browser reconnects on its own if the connection drops.
export function eventsURL(): string {
  return `${apiBaseURL()}/api/events`
}

// request performs one API call. A 204 has no body. Any other response is JSON.
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${apiBaseURL()}${path}`, {
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
    },
  })
  if (response.status === 204) {
    return undefined as T
  }
  const text = await response.text()
  const body = text === "" ? null : (JSON.parse(text) as { error?: string })
  if (!response.ok) {
    throw new ApiError(response.status, body?.error ?? response.statusText)
  }
  return body as T
}

// listMonitors returns every monitor with status, last latency, and 24 hour uptime.
export function listMonitors(): Promise<Monitor[]> {
  return request<Monitor[]>("/api/monitors")
}

// listChecks returns recent probes for one monitor, oldest of that window first.
export function listChecks(monitorID: number, limit = 100): Promise<Check[]> {
  return request<Check[]>(`/api/monitors/${monitorID}/checks?limit=${limit}`)
}

// listIncidents returns incidents. Omitting status asks for every incident in one request.
export function listIncidents(status?: IncidentStatus): Promise<Incident[]> {
  const query = status ? `?status=${status}` : ""
  return request<Incident[]>(`/api/incidents${query}`)
}

// createMonitor posts a new monitor. The server validates the body and may respond 400.
export function createMonitor(input: NewMonitor): Promise<Monitor> {
  return request<Monitor>("/api/monitors", {
    method: "POST",
    body: JSON.stringify(input),
  })
}

// deleteMonitor removes a monitor. The server responds 204 with an empty body.
export function deleteMonitor(id: number): Promise<void> {
  return request<void>(`/api/monitors/${id}`, { method: "DELETE" })
}

// ackIncident marks an open incident as acknowledged.
export function ackIncident(id: number): Promise<Incident> {
  return request<Incident>(`/api/incidents/${id}/ack`, { method: "POST" })
}

// resolveIncident closes an open or acknowledged incident.
export function resolveIncident(id: number): Promise<Incident> {
  return request<Incident>(`/api/incidents/${id}/resolve`, { method: "POST" })
}
