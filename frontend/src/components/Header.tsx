import { downCount, overallStatus } from "../format.ts"
import type { Monitor } from "../types.ts"

type HeaderProps = {
  monitors: Monitor[]
  updatedAt: string | null
}

export function Header({ monitors, updatedAt }: HeaderProps) {
  const down = downCount(monitors)
  const tone = monitors.length === 0 ? "unknown" : down > 0 ? "down" : "up"
  return (
    <header className="topbar">
      <div className="topbar-inner">
        <div>
          <p className="brand">Uptime</p>
          <p className="brand-sub">URL checks, every interval</p>
        </div>
        <div className={`pill pill-${tone}`} role="status" aria-live="polite">
          <span className={`status-dot ${tone}`} aria-hidden="true" />
          {overallStatus(monitors)}
        </div>
        <p className="updated">{updatedAt ? `Updated ${updatedAt}` : "Connecting"}</p>
      </div>
    </header>
  )
}
