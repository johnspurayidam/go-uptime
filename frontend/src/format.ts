import type { Monitor } from "./types.ts"

export function formatTimestamp(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return iso
  }
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  })
}

export function formatClock(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return iso
  }
  return date.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

export function formatLatency(ms: number | null): string {
  if (ms === null) {
    return "—"
  }
  return `${ms} ms`
}

export function formatUptime(percent: number | null): string {
  if (percent === null) {
    return "—"
  }
  return `${percent}%`
}

export function overallStatus(monitors: Monitor[]): string {
  if (monitors.length === 0) {
    return "No monitors"
  }
  const down = monitors.filter((monitor) => monitor.status === "down").length
  if (down === 0) {
    return "All operational"
  }
  if (down === 1) {
    return "1 monitor down"
  }
  return `${down} monitors down`
}

export function downCount(monitors: Monitor[]): number {
  return monitors.filter((monitor) => monitor.status === "down").length
}
