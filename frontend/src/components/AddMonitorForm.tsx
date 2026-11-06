import { useState, type FormEvent } from "react"
import type { NewMonitor } from "../types.ts"

type AddMonitorFormProps = {
  onSubmit: (input: NewMonitor) => Promise<void>
  error: string | null
}

export function AddMonitorForm({ onSubmit, error }: AddMonitorFormProps) {
  const [name, setName] = useState("")
  const [url, setUrl] = useState("")
  const [intervalSeconds, setIntervalSeconds] = useState("30")
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const interval = Number(intervalSeconds)
    setSubmitting(true)
    try {
      await onSubmit({
        name,
        url,
        interval_seconds: Number.isFinite(interval) ? interval : undefined,
      })
      setName("")
      setUrl("")
      setIntervalSeconds("30")
    } catch {
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form className="add-form" onSubmit={(event) => void handleSubmit(event)}>
      <h2>Add a monitor</h2>
      <div className="add-fields">
        <label>
          Name
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            required
            maxLength={100}
            placeholder="Docs"
          />
        </label>
        <label>
          URL
          <input
            value={url}
            onChange={(event) => setUrl(event.target.value)}
            required
            type="url"
            placeholder="https://example.com"
          />
        </label>
        <label>
          Interval (seconds)
          <input
            value={intervalSeconds}
            onChange={(event) => setIntervalSeconds(event.target.value)}
            required
            type="number"
            min={5}
            max={86400}
          />
        </label>
        <button type="submit" className="button primary" disabled={submitting}>
          {submitting ? "Adding…" : "Add"}
        </button>
      </div>
      {error ? <p className="form-error">{error}</p> : null}
    </form>
  )
}
