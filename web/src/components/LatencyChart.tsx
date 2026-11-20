import { useMemo, useState } from 'preact/hooks';
import type { Heartbeat, MonitorRange, ResponsePoint } from '../types';
import { formatShortTime } from '../utils/format';

interface LatencyChartProps {
  heartbeats: Heartbeat[];
  degradedMs?: number;
  range?: MonitorRange;
  onRangeChange?: (r: MonitorRange) => void;
  loading?: boolean;
}

const RANGES: { key: MonitorRange; label: string }[] = [
  { key: '1h', label: '1 h' },
  { key: '24h', label: '24 h' },
  { key: '7d', label: '7 d' },
  { key: '30d', label: '30 d' },
];

export function LatencyChart({
  heartbeats,
  degradedMs = 0,
  range,
  onRangeChange,
  loading,
}: LatencyChartProps) {
  const [activeIndex, setActiveIndex] = useState<number | null>(null);

  const series = useMemo(() => {
    return (heartbeats || [])
      .slice()
      .sort((a, b) => new Date(a.checked_at).getTime() - new Date(b.checked_at).getTime());
  }, [heartbeats]);

  const headerControls = (
    <div class="chart-header">
      <div>
        <h3 class="chart-title">Response time</h3>
        <span class="form-help">
          {degradedMs > 0
            ? `Dashed line marks the ${degradedMs} ms degraded threshold`
            : 'Milliseconds per check'}
        </span>
      </div>
      {range && onRangeChange ? (
        <div class="range-pills" role="group" aria-label="Response time window">
          {RANGES.map((r) => (
            <button
              key={r.key}
              type="button"
              class={`range-pill${range === r.key ? ' range-pill--active' : ''}`}
              aria-pressed={range === r.key}
              onClick={() => onRangeChange(r.key)}
            >
              {r.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );

  if (loading) {
    return (
      <div class="chart-box">
        {headerControls}
        <p class="form-help" role="status" style={{ margin: '12px 0 0' }}>
          Loading response time samples...
        </p>
      </div>
    );
  }

  if (series.length < 2) {
    return (
      <div class="chart-box">
        {headerControls}
        <p class="form-help" style={{ margin: '12px 0 0' }}>
          Not enough check samples yet to plot response time. Run Check now or wait for two scheduled checks.
        </p>
      </div>
    );
  }

  const width = 680;
  const height = 188;
  const padL = 48;
  const padR = 16;
  const padT = 14;
  const padB = 26;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const latencies = series.map((h) => Math.max(0, h.latency_ms));
  const minMs = Math.min(...latencies);
  const maxMs = Math.max(...latencies, 1);
  const avgMs = Math.round(latencies.reduce((acc, v) => acc + v, 0) / latencies.length);
  const yMax = Math.max(maxMs, degradedMs > 0 ? Math.ceil(degradedMs * 1.15) : 1, Math.ceil(maxMs * 1.1));

  const points = series.map((h, i) => {
    const x = padL + (i / (series.length - 1)) * plotW;
    const y = padT + plotH - (Math.max(0, h.latency_ms) / yMax) * plotH;
    return { x, y, hb: h };
  });

  const linePath = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ');
  const areaPath = `${linePath} L${points[points.length - 1].x.toFixed(1)},${(padT + plotH).toFixed(1)} L${points[0].x.toFixed(1)},${(padT + plotH).toFixed(1)} Z`;
  const thresholdY =
    degradedMs > 0 ? padT + plotH - (Math.min(degradedMs, yMax) / yMax) * plotH : null;

  const selected = activeIndex !== null ? points[activeIndex] : null;

  return (
    <div class="chart-box">
      {headerControls}

      <div class="chart-stats-bar mono">
        <span>
          min {minMs} ms | avg {avgMs} ms | max {maxMs} ms
        </span>
        {selected ? (
          <span>
            {formatShortTime(selected.hb.checked_at)}: {selected.hb.latency_ms} ms ({selected.hb.status})
          </span>
        ) : null}
      </div>

      <svg
        viewBox={`0 0 ${width} ${height}`}
        style={{ width: '100%', height: 'auto', display: 'block' }}
        role="img"
        aria-label={`Response time chart from ${series.length} checks, average ${avgMs} milliseconds`}
      >
        <line x1={padL} y1={padT} x2={padL} y2={padT + plotH} stroke="var(--border-strong)" stroke-width="1" />
        <line
          x1={padL}
          y1={padT + plotH}
          x2={width - padR}
          y2={padT + plotH}
          stroke="var(--border-strong)"
          stroke-width="1"
        />
        <line
          x1={padL}
          y1={padT + plotH / 2}
          x2={width - padR}
          y2={padT + plotH / 2}
          stroke="var(--border)"
          stroke-dasharray="3 3"
          stroke-width="1"
        />

        {thresholdY !== null ? (
          <line
            x1={padL}
            y1={thresholdY}
            x2={width - padR}
            y2={thresholdY}
            stroke="var(--status-degraded)"
            stroke-dasharray="5 4"
            stroke-width="1.2"
          />
        ) : null}

        <text x={padL - 8} y={padT + 4} text-anchor="end" font-size="10" fill="var(--text-muted)" class="mono">
          {yMax}
        </text>
        <text
          x={padL - 8}
          y={padT + plotH / 2 + 4}
          text-anchor="end"
          font-size="10"
          fill="var(--text-muted)"
          class="mono"
        >
          {Math.round(yMax / 2)}
        </text>
        <text x={padL - 8} y={padT + plotH} text-anchor="end" font-size="10" fill="var(--text-muted)" class="mono">
          0
        </text>

        <path d={areaPath} fill="var(--text)" fill-opacity="0.08" />
        <path d={linePath} fill="none" stroke="var(--text)" stroke-width="1.8" stroke-linejoin="round" />

        {points.map((p, idx) => (
          <circle
            key={p.hb.id}
            cx={p.x}
            cy={p.y}
            r={activeIndex === idx ? 4.5 : 2.2}
            fill={
              p.hb.status === 'down'
                ? 'var(--status-down)'
                : p.hb.status === 'degraded'
                  ? 'var(--status-degraded)'
                  : 'var(--text)'
            }
            style={{ cursor: 'pointer' }}
            onMouseEnter={() => setActiveIndex(idx)}
            onMouseLeave={() => setActiveIndex(null)}
            onClick={() => setActiveIndex((cur) => (cur === idx ? null : idx))}
          />
        ))}

        <text x={padL} y={height - 6} text-anchor="start" font-size="10" fill="var(--text-muted)" class="mono">
          {formatShortTime(series[0].checked_at)}
        </text>
        <text
          x={width - padR}
          y={height - 6}
          text-anchor="end"
          font-size="10"
          fill="var(--text-muted)"
          class="mono"
        >
          {formatShortTime(series[series.length - 1].checked_at)}
        </text>
      </svg>
    </div>
  );
}

export function OverviewResponseChart({ points }: { points: ResponsePoint[] }) {
  const valid = (points || []).filter((p): p is { timestamp: string; median_ms: number } => p.median_ms !== null);
  if (valid.length < 2) {
    return (
      <p class="form-help" style={{ margin: '8px 0 0' }}>
        Not enough 24h check data yet to plot median response time.
      </p>
    );
  }

  const width = 340;
  const height = 132;
  const padL = 38;
  const padR = 12;
  const padT = 10;
  const padB = 22;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const vals = valid.map((p) => Math.max(0, p.median_ms));
  const yMax = Math.max(...vals, 10);

  const coords = valid.map((p, i) => ({
    x: padL + (i / (valid.length - 1)) * plotW,
    y: padT + plotH - (Math.max(0, p.median_ms) / yMax) * plotH,
  }));

  const linePath = coords.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ');
  const areaPath = `${linePath} L${coords[coords.length - 1].x.toFixed(1)},${(padT + plotH).toFixed(1)} L${coords[0].x.toFixed(1)},${(padT + plotH).toFixed(1)} Z`;

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      style={{ width: '100%', height: 'auto', display: 'block' }}
      role="img"
      aria-label="Median response time over the last 24 hours"
    >
      <line x1={padL} y1={padT} x2={padL} y2={padT + plotH} stroke="var(--border)" stroke-width="1" />
      <line x1={padL} y1={padT + plotH} x2={width - padR} y2={padT + plotH} stroke="var(--border)" stroke-width="1" />
      <text x={padL - 6} y={padT + 4} text-anchor="end" font-size="10" fill="var(--text-caption)" class="mono">
        {yMax}
      </text>
      <text x={padL - 6} y={padT + plotH} text-anchor="end" font-size="10" fill="var(--text-caption)" class="mono">
        0
      </text>
      <path d={areaPath} fill="var(--text)" fill-opacity="0.08" />
      <path d={linePath} fill="none" stroke="var(--text)" stroke-width="1.6" stroke-linejoin="round" />
      <text x={padL} y={height - 5} text-anchor="start" font-size="10" fill="var(--text-caption)" class="mono">
        {formatShortTime(valid[0].timestamp)}
      </text>
      <text x={width - padR} y={height - 5} text-anchor="end" font-size="10" fill="var(--text-caption)" class="mono">
        {formatShortTime(valid[valid.length - 1].timestamp)}
      </text>
    </svg>
  );
}
