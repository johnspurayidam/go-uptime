import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { formatClock, formatTimestamp } from "../format.ts"
import type { Check } from "../types.ts"

type LatencyChartProps = {
  monitorName: string | null
  checks: Check[]
}

export function LatencyChart({ monitorName, checks }: LatencyChartProps) {
  return (
    <section className="card chart-card">
      <h2>{monitorName ? `${monitorName} latency` : "Latency"}</h2>
      {monitorName === null ? (
        <p className="empty">Select a monitor to see its recent checks.</p>
      ) : checks.length === 0 ? (
        <p className="empty">No checks recorded yet.</p>
      ) : (
        <div className="chart-frame">
          <ResponsiveContainer width="100%" height={260}>
            <LineChart data={checks} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
              <CartesianGrid stroke="#e2dcd0" vertical={false} />
              <XAxis
                dataKey="checked_at"
                tickFormatter={formatClock}
                minTickGap={28}
                tick={{ fill: "#5e5a52", fontSize: 12 }}
                axisLine={{ stroke: "#e2dcd0" }}
                tickLine={false}
              />
              <YAxis
                unit=" ms"
                width={64}
                tick={{ fill: "#5e5a52", fontSize: 12 }}
                axisLine={false}
                tickLine={false}
              />
              <Tooltip
                labelFormatter={(label) => formatTimestamp(String(label))}
                formatter={(value) => [`${value} ms`, "Latency"]}
              />
              <Line
                type="monotone"
                dataKey="latency_ms"
                stroke="#1f4b3a"
                strokeWidth={2}
                dot={{ r: 3, fill: "#1f4b3a" }}
                activeDot={{ r: 5 }}
                isAnimationActive={false}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      )}
    </section>
  )
}
