import { useId, useMemo, useRef, useState } from 'preact/hooks';
import type { Heartbeat, MonitorRange, ResponsePoint } from '../types';
import { formatShortTime } from '../utils/format';

interface ChartSample {
  id: string | number;
  timestamp: string;
  valueMs: number;
  statusColor: string;
  statusLabel: string;
  message?: string;
}

interface PlotPoint extends ChartSample {
  x: number;
  y: number;
}

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

function clamp(v: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, v));
}

function formatTooltipTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** Fritsch-Carlson monotone cubic Bezier spline clamped within [yMin, yMax]. */
function buildSmoothPath(points: { x: number; y: number }[], yMin: number, yMax: number): string {
  const n = points.length;
  if (n === 0) return '';
  if (n === 1) return `M${points[0].x.toFixed(1)},${points[0].y.toFixed(1)}`;
  if (n === 2) {
    return `M${points[0].x.toFixed(1)},${points[0].y.toFixed(1)} L${points[1].x.toFixed(1)},${points[1].y.toFixed(1)}`;
  }

  const dx: number[] = [];
  const d: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    const stepX = points[i + 1].x - points[i].x;
    dx.push(stepX);
    d.push(stepX > 1e-9 ? (points[i + 1].y - points[i].y) / stepX : 0);
  }

  const m = new Array<number>(n).fill(0);
  m[0] = d[0];
  m[n - 1] = d[n - 2];
  for (let i = 1; i < n - 1; i++) {
    if (d[i - 1] * d[i] <= 0) {
      m[i] = 0;
    } else {
      const w1 = 2 * dx[i] + dx[i - 1];
      const w2 = dx[i] + 2 * dx[i - 1];
      m[i] = (w1 + w2) / (w1 / d[i - 1] + w2 / d[i]);
    }
  }

  for (let i = 0; i < n - 1; i++) {
    if (Math.abs(d[i]) < 1e-9) {
      m[i] = 0;
      m[i + 1] = 0;
    } else {
      const alpha = m[i] / d[i];
      const beta = m[i + 1] / d[i];
      const r2 = alpha * alpha + beta * beta;
      if (r2 > 9) {
        const tau = 3 / Math.sqrt(r2);
        m[i] = tau * alpha * d[i];
        m[i + 1] = tau * beta * d[i];
      }
    }
  }

  const parts = [`M${points[0].x.toFixed(1)},${points[0].y.toFixed(1)}`];
  for (let i = 0; i < n - 1; i++) {
    const step = dx[i] / 3;
    const cp1x = points[i].x + step;
    const cp1y = clamp(points[i].y + m[i] * step, yMin, yMax);
    const cp2x = points[i + 1].x - step;
    const cp2y = clamp(points[i + 1].y - m[i + 1] * step, yMin, yMax);
    parts.push(
      `C${cp1x.toFixed(1)},${cp1y.toFixed(1)} ${cp2x.toFixed(1)},${cp2y.toFixed(1)} ${points[i + 1].x.toFixed(1)},${points[i + 1].y.toFixed(1)}`,
    );
  }
  return parts.join(' ');
}

function buildYTicks(yMax: number, count: number): number[] {
  const ticks: number[] = [];
  for (let i = 0; i <= count; i++) {
    const val = Math.round((yMax * i) / count);
    if (!ticks.includes(val)) ticks.push(val);
  }
  return ticks;
}

function buildXTicks(total: number, maxTicks: number): number[] {
  if (total <= 1) return [0];
  const steps = Math.min(maxTicks - 1, total - 1);
  const indices: number[] = [];
  for (let i = 0; i <= steps; i++) {
    const idx = Math.round((i / steps) * (total - 1));
    if (!indices.includes(idx)) indices.push(idx);
  }
  return indices;
}

interface SmoothAreaSvgProps {
  samples: ChartSample[];
  width: number;
  height: number;
  padL: number;
  padR: number;
  padT: number;
  padB: number;
  degradedMs?: number;
  xTickCount: number;
  ariaLabel: string;
}

function SmoothAreaSvg({
  samples,
  width,
  height,
  padL,
  padR,
  padT,
  padB,
  degradedMs = 0,
  xTickCount,
  ariaLabel,
}: SmoothAreaSvgProps) {
  const gradId = useId();
  const svgRef = useRef<SVGSVGElement>(null);
  const [activeIndex, setActiveIndex] = useState<number | null>(null);

  const plotW = width - padL - padR;
  const plotH = height - padT - padB;
  const maxVal = Math.max(...samples.map((s) => s.valueMs), 1);
  const yMax = Math.max(
    maxVal,
    degradedMs > 0 ? Math.ceil(degradedMs * 1.15) : 1,
    Math.ceil(maxVal * 1.1),
  );

  const points: PlotPoint[] = useMemo(() => {
    return samples.map((s, i) => ({
      ...s,
      x: padL + (samples.length > 1 ? (i / (samples.length - 1)) * plotW : plotW / 2),
      y: padT + plotH - (clamp(s.valueMs, 0, yMax) / yMax) * plotH,
    }));
  }, [samples, padL, padT, plotW, plotH, yMax]);

  const linePath = useMemo(() => buildSmoothPath(points, padT, padT + plotH), [points, padT, plotH]);
  const baseLineY = (padT + plotH).toFixed(1);
  const areaPath =
    points.length >= 2
      ? `${linePath} L${points[points.length - 1].x.toFixed(1)},${baseLineY} L${points[0].x.toFixed(1)},${baseLineY} Z`
      : '';

  const yTicks = buildYTicks(yMax, 4);
  const xTickIndices = buildXTicks(points.length, xTickCount);
  const thresholdY =
    degradedMs > 0 ? padT + plotH - (Math.min(degradedMs, yMax) / yMax) * plotH : null;

  const selected = activeIndex !== null && points[activeIndex] ? points[activeIndex] : null;
  const flipLeft = selected ? selected.x > padL + plotW * 0.65 : false;

  const updateFromClientX = (clientX: number) => {
    const svg = svgRef.current;
    if (!svg || points.length === 0) return;
    const rect = svg.getBoundingClientRect();
    if (rect.width <= 0) return;
    const svgX = ((clientX - rect.left) / rect.width) * width;
    const ratio = clamp((svgX - padL) / plotW, 0, 1);
    const idx = Math.round(ratio * (points.length - 1));
    setActiveIndex(clamp(idx, 0, points.length - 1));
  };

  return (
    <div
      class="chart-plot-wrap"
      tabIndex={0}
      aria-label={ariaLabel}
      onMouseLeave={() => setActiveIndex(null)}
      onBlur={() => setActiveIndex(null)}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          setActiveIndex(null);
        } else if (e.key === 'ArrowRight') {
          e.preventDefault();
          setActiveIndex((cur) => (cur === null ? 0 : Math.min(points.length - 1, cur + 1)));
        } else if (e.key === 'ArrowLeft') {
          e.preventDefault();
          setActiveIndex((cur) => (cur === null ? points.length - 1 : Math.max(0, cur - 1)));
        }
      }}
    >
      <svg
        ref={svgRef}
        viewBox={`0 0 ${width} ${height}`}
        style={{ width: '100%', height: 'auto', display: 'block' }}
        role="img"
        aria-label={ariaLabel}
      >
        <defs>
          <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="var(--status-up)" stop-opacity="0.24" />
            <stop offset="100%" stop-color="var(--status-up)" stop-opacity="0.01" />
          </linearGradient>
        </defs>

        {yTicks.map((val) => {
          const y = padT + plotH - (val / yMax) * plotH;
          return (
            <g key={`y-${val}`}>
              <line
                x1={padL}
                y1={y}
                x2={width - padR}
                y2={y}
                stroke={val === 0 ? 'var(--border-strong)' : 'var(--border)'}
                stroke-dasharray={val === 0 ? undefined : '2 3'}
                stroke-width="1"
              />
              <text
                x={padL - 8}
                y={y + 3.5}
                text-anchor="end"
                font-size="10"
                fill="var(--text-muted)"
                class="mono"
              >
                {val}
              </text>
            </g>
          );
        })}

        {xTickIndices.map((idx, tickPos) => {
          const pt = points[idx];
          const anchor =
            tickPos === 0 ? 'start' : tickPos === xTickIndices.length - 1 ? 'end' : 'middle';
          return (
            <g key={`x-${idx}`}>
              {tickPos > 0 && tickPos < xTickIndices.length - 1 ? (
                <line
                  x1={pt.x}
                  y1={padT}
                  x2={pt.x}
                  y2={padT + plotH}
                  stroke="var(--border)"
                  stroke-dasharray="2 3"
                  stroke-width="1"
                />
              ) : null}
              <text
                x={pt.x}
                y={height - 6}
                text-anchor={anchor}
                font-size="10"
                fill="var(--text-muted)"
                class="mono"
              >
                {formatShortTime(pt.timestamp)}
              </text>
            </g>
          );
        })}

        <line x1={padL} y1={padT} x2={padL} y2={padT + plotH} stroke="var(--border-strong)" stroke-width="1" />

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

        {areaPath ? <path d={areaPath} fill={`url(#${gradId})`} /> : null}
        <path
          d={linePath}
          fill="none"
          stroke="var(--status-up)"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        />

        {selected ? (
          <g pointer-events="none">
            <line
              x1={selected.x}
              y1={padT}
              x2={selected.x}
              y2={padT + plotH}
              stroke="var(--border-strong)"
              stroke-dasharray="3 3"
              stroke-width="1"
            />
            <circle
              cx={selected.x}
              cy={selected.y}
              r="4.5"
              fill="var(--surface)"
              stroke={selected.statusColor}
              stroke-width="2"
            />
          </g>
        ) : null}

        <rect
          x={padL}
          y={padT}
          width={plotW}
          height={plotH}
          fill="transparent"
          style={{ cursor: 'crosshair', touchAction: 'pan-y' }}
          onPointerMove={(e) => updateFromClientX(e.clientX)}
          onPointerDown={(e) => updateFromClientX(e.clientX)}
          onPointerLeave={() => setActiveIndex(null)}
          onTouchStart={(e) => {
            if (e.touches[0]) updateFromClientX(e.touches[0].clientX);
          }}
          onTouchMove={(e) => {
            if (e.touches[0]) updateFromClientX(e.touches[0].clientX);
          }}
        />
      </svg>

      {selected ? (
        <div
          class={`chart-tooltip${flipLeft ? ' chart-tooltip--left' : ''}`}
          style={{ left: `${((selected.x / width) * 100).toFixed(2)}%` }}
          role="status"
        >
          <div class="mono">{formatTooltipTimestamp(selected.timestamp)}</div>
          <div class="chart-tooltip-row">
            <span
              class="chart-tooltip-swatch"
              style={{ backgroundColor: selected.statusColor }}
              aria-hidden="true"
            />
            <strong class="mono">{selected.valueMs} ms</strong>
            <span>({selected.statusLabel})</span>
          </div>
          {selected.message ? <div class="chart-tooltip-msg">{selected.message}</div> : null}
        </div>
      ) : null}
    </div>
  );
}

export function LatencyChart({
  heartbeats,
  degradedMs = 0,
  range,
  onRangeChange,
  loading,
}: LatencyChartProps) {
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

  const latencies = series.map((h) => Math.max(0, h.latency_ms));
  const minMs = Math.min(...latencies);
  const maxMs = Math.max(...latencies, 1);
  const avgMs = Math.round(latencies.reduce((acc, v) => acc + v, 0) / latencies.length);

  const samples: ChartSample[] = series.map((h) => ({
    id: h.id,
    timestamp: h.checked_at,
    valueMs: Math.max(0, h.latency_ms),
    statusColor:
      h.status === 'down'
        ? 'var(--status-down)'
        : h.status === 'degraded'
          ? 'var(--status-degraded)'
          : 'var(--status-up)',
    statusLabel: h.status === 'down' ? 'Down' : h.status === 'degraded' ? 'Degraded' : 'Up',
    message: h.status !== 'up' && h.message ? h.message : undefined,
  }));

  return (
    <div class="chart-box">
      {headerControls}
      <div class="chart-stats-bar mono">
        <span>
          min {minMs} ms | avg {avgMs} ms | max {maxMs} ms
        </span>
      </div>
      <SmoothAreaSvg
        samples={samples}
        width={680}
        height={196}
        padL={48}
        padR={16}
        padT={14}
        padB={26}
        degradedMs={degradedMs}
        xTickCount={5}
        ariaLabel={`Response time chart from ${series.length} checks, average ${avgMs} milliseconds`}
      />
    </div>
  );
}

export function OverviewResponseChart({ points }: { points: ResponsePoint[] }) {
  const valid = useMemo(
    () =>
      (points || []).filter(
        (p): p is { timestamp: string; median_ms: number } => p.median_ms !== null,
      ),
    [points],
  );

  if (valid.length < 2) {
    return (
      <p class="form-help" style={{ margin: '8px 0 0' }}>
        Not enough 24h check data yet to plot median response time.
      </p>
    );
  }

  const samples: ChartSample[] = valid.map((p, idx) => ({
    id: `${p.timestamp}-${idx}`,
    timestamp: p.timestamp,
    valueMs: Math.max(0, p.median_ms),
    statusColor: 'var(--status-up)',
    statusLabel: 'Median',
  }));

  return (
    <SmoothAreaSvg
      samples={samples}
      width={340}
      height={146}
      padL={40}
      padR={12}
      padT={10}
      padB={24}
      xTickCount={4}
      ariaLabel="Median response time over the last 24 hours"
    />
  );
}
