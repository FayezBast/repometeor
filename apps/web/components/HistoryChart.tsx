"use client";

import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { HistoryResponse } from "@/lib/types";

export function HistoryChart({ points }: { points: HistoryResponse["data"] }) {
  const data = points.map((point) => ({
    ...point,
    label: new Intl.DateTimeFormat("en", {
      month: "short",
      day: "numeric",
    }).format(new Date(point.at)),
  }));

  return (
    <div
      className="history-chart"
      role="img"
      aria-label="Repository star history over time"
    >
      <ResponsiveContainer width="100%" height={300}>
        <AreaChart
          data={data}
          margin={{ top: 14, right: 8, left: 0, bottom: 0 }}
        >
          <defs>
            <linearGradient id="stars" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#ff6b32" stopOpacity={0.44} />
              <stop offset="100%" stopColor="#ff6b32" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke="rgba(159, 174, 193, .14)" vertical={false} />
          <XAxis
            dataKey="label"
            tickLine={false}
            axisLine={false}
            minTickGap={40}
          />
          <YAxis
            tickLine={false}
            axisLine={false}
            width={54}
            domain={["dataMin", "dataMax"]}
          />
          <Tooltip
            contentStyle={{
              background: "#111722",
              border: "1px solid #303846",
              borderRadius: 12,
            }}
          />
          <Area
            type="monotone"
            dataKey="stars"
            stroke="#ff6b32"
            fill="url(#stars)"
            strokeWidth={3}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
