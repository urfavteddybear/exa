import { useEffect, useRef, useState } from 'preact/hooks';
import type { Heartbeat, OverviewBucket, PublicDay } from '../types';
import {
  dayStatusInfo,
  formatDateTime,
  formatDayLabel,
  formatDuration,
  formatShortTime,
  heartbeatStatusInfo,
} from '../utils/format';
import { CloseIcon } from './Icons';
import { StatusBadge } from './StatusBadge';

function usePopoverDismiss(wrapRef: { current: HTMLDivElement | null }, pinned: boolean, onClose: () => void) {
  useEffect(() => {
    if (!pinned) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    const onPointerDown = (e: MouseEvent | TouchEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        onClose();
      }
    };
    window.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('touchstart', onPointerDown);
    return () => {
      window.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('touchstart', onPointerDown);
    };
  }, [wrapRef, pinned, onClose]);
}

function popoverPlacement(idx: number | null, total: number) {
  const rawPct = idx !== null && total > 0 ? ((idx + 0.5) / total) * 100 : 50;
  const cls =
    rawPct < 25
      ? 'day-popover day-popover--left'
      : rawPct > 75
        ? 'day-popover day-popover--right'
        : 'day-popover';
  const style = rawPct >= 25 && rawPct <= 75 ? { left: `${rawPct}%` } : undefined;
  return { cls, style };
}

interface UptimeBarProps {
  monitorName: string;
  days: PublicDay[];
  uptime90d: number | null;
}

export function UptimeBar({ monitorName, days, uptime90d }: UptimeBarProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [pinnedIdx, setPinnedIdx] = useState<number | null>(null);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  usePopoverDismiss(wrapRef, pinnedIdx !== null, () => setPinnedIdx(null));

  const activeIdx = pinnedIdx !== null ? pinnedIdx : hoverIdx;
  const activeDay = activeIdx !== null ? days[activeIdx] : null;
  const { cls, style } = popoverPlacement(activeIdx, days.length);

  const uptimeText =
    uptime90d !== null && Number.isFinite(uptime90d) ? `${uptime90d.toFixed(2)}% uptime` : 'No data yet';

  return (
    <div class="uptime-bar-wrap" ref={wrapRef}>
      <div class="uptime-bars" role="group" aria-label={`90-day uptime history for ${monitorName}`}>
        {days.map((d, idx) => {
          const info = dayStatusInfo(d.status);
          const isPinned = pinnedIdx === idx;
          const pctStr =
            d.uptime_pct !== null && Number.isFinite(d.uptime_pct)
              ? `${d.uptime_pct.toFixed(2)}%`
              : 'no data';
          return (
            <button
              key={d.day}
              type="button"
              class={`uptime-bar-seg uptime-bar-seg--${info.tone}`}
              aria-label={`${formatDayLabel(d.day)}: ${info.label}, ${pctStr}`}
              aria-expanded={isPinned}
              onMouseEnter={() => {
                if (pinnedIdx === null) setHoverIdx(idx);
              }}
              onMouseLeave={() => {
                if (pinnedIdx === null) setHoverIdx(null);
              }}
              onClick={() => {
                setPinnedIdx((cur) => (cur === idx ? null : idx));
                setHoverIdx(null);
              }}
            />
          );
        })}
      </div>

      {activeDay ? (
        <div
          class={cls}
          style={style}
          role={pinnedIdx !== null ? 'dialog' : 'status'}
          aria-label={`Uptime details for ${formatDayLabel(activeDay.day)}`}
        >
          <div class="day-popover-head">
            <strong>{formatDayLabel(activeDay.day)}</strong>
            {pinnedIdx !== null ? (
              <button
                type="button"
                class="day-popover-close"
                aria-label="Close day details"
                onClick={() => setPinnedIdx(null)}
              >
                <CloseIcon size={14} />
              </button>
            ) : null}
          </div>
          <StatusBadge
            tone={dayStatusInfo(activeDay.status).tone}
            label={dayStatusInfo(activeDay.status).label}
            size={15}
          />
          <dl class="day-popover-stats">
            <dt>Uptime</dt>
            <dd class="mono">
              {activeDay.uptime_pct !== null && Number.isFinite(activeDay.uptime_pct)
                ? `${activeDay.uptime_pct.toFixed(2)}%`
                : 'No data'}
            </dd>
            <dt>Checks</dt>
            <dd class="mono">
              {activeDay.total_checks} ({activeDay.down_checks} down)
            </dd>
            <dt>Downtime</dt>
            <dd class="mono">{formatDuration(activeDay.downtime_sec)}</dd>
            <dt>Avg latency</dt>
            <dd class="mono">
              {activeDay.total_checks > 0 ? `${Math.round(activeDay.avg_latency_ms)} ms` : 'N/A'}
            </dd>
          </dl>
          {activeDay.incidents && activeDay.incidents.length > 0 ? (
            <ul class="day-popover-incidents">
              {activeDay.incidents.map((inc) => (
                <li key={inc.id}>
                  <strong>{inc.title}</strong> ({inc.impact})
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      <div class="uptime-bar-legend">
        <span>90 days ago</span>
        <span class="uptime-bar-legend-line" aria-hidden="true" />
        <span class="mono">{uptimeText}</span>
        <span class="uptime-bar-legend-line" aria-hidden="true" />
        <span>Today</span>
      </div>
    </div>
  );
}

export function Availability60Bar({ buckets }: { buckets: OverviewBucket[] }) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [pinnedIdx, setPinnedIdx] = useState<number | null>(null);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  usePopoverDismiss(wrapRef, pinnedIdx !== null, () => setPinnedIdx(null));

  const items = buckets || [];
  const activeIdx = pinnedIdx !== null ? pinnedIdx : hoverIdx;
  const activeBucket = activeIdx !== null ? items[activeIdx] : null;
  const { cls, style } = popoverPlacement(activeIdx, items.length);

  if (items.length === 0) {
    return <p class="form-help">No check windows recorded in the last 24 hours.</p>;
  }

  return (
    <div class="uptime-bar-wrap" ref={wrapRef}>
      <div
        class="uptime-bars uptime-bars--60"
        style={{ gridTemplateColumns: `repeat(${items.length}, minmax(0, 1fr))` }}
        role="group"
        aria-label="Availability across all monitors over the last 24 hours"
      >
        {items.map((b, idx) => {
          const info = dayStatusInfo(b.status);
          return (
            <button
              key={b.timestamp}
              type="button"
              class={`uptime-bar-seg uptime-bar-seg--${info.tone}`}
              aria-label={`${formatShortTime(b.timestamp)}: ${info.label}, ${b.total_checks} checks`}
              aria-expanded={pinnedIdx === idx}
              onMouseEnter={() => {
                if (pinnedIdx === null) setHoverIdx(idx);
              }}
              onMouseLeave={() => {
                if (pinnedIdx === null) setHoverIdx(null);
              }}
              onClick={() => {
                setPinnedIdx((cur) => (cur === idx ? null : idx));
                setHoverIdx(null);
              }}
            />
          );
        })}
      </div>

      {activeBucket ? (
        <div
          class={cls}
          style={style}
          role={pinnedIdx !== null ? 'dialog' : 'status'}
          aria-label={`Window details at ${formatShortTime(activeBucket.timestamp)}`}
        >
          <div class="day-popover-head">
            <strong class="mono">{formatDateTime(activeBucket.timestamp)}</strong>
            {pinnedIdx !== null ? (
              <button
                type="button"
                class="day-popover-close"
                aria-label="Close window details"
                onClick={() => setPinnedIdx(null)}
              >
                <CloseIcon size={14} />
              </button>
            ) : null}
          </div>
          <StatusBadge
            tone={dayStatusInfo(activeBucket.status).tone}
            label={dayStatusInfo(activeBucket.status).label}
            size={15}
          />
          <dl class="day-popover-stats">
            <dt>Availability</dt>
            <dd class="mono">
              {activeBucket.uptime_pct !== null ? `${activeBucket.uptime_pct.toFixed(2)}%` : 'No data'}
            </dd>
            <dt>Checks</dt>
            <dd class="mono">
              {activeBucket.total_checks} ({activeBucket.down_checks} down)
            </dd>
          </dl>
        </div>
      ) : null}
    </div>
  );
}

export function Heartbeat60Strip({
  checks,
  intervalSec,
}: {
  checks: Heartbeat[];
  intervalSec: number;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [pinnedIdx, setPinnedIdx] = useState<number | null>(null);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  usePopoverDismiss(wrapRef, pinnedIdx !== null, () => setPinnedIdx(null));

  const slots = 60;
  const padCount = Math.max(0, slots - checks.length);
  const padded: Array<Heartbeat | null> = [
    ...Array.from({ length: padCount }, () => null),
    ...checks.slice(-slots),
  ];

  const activeIdx = pinnedIdx !== null ? pinnedIdx : hoverIdx;
  const activeHb = activeIdx !== null ? padded[activeIdx] : null;
  const { cls, style } = popoverPlacement(activeIdx, padded.length);

  return (
    <div class="uptime-bar-wrap" ref={wrapRef}>
      <div
        class="uptime-bars uptime-bars--60"
        style={{ gridTemplateColumns: `repeat(${slots}, minmax(0, 1fr))` }}
        role="group"
        aria-label="Last 60 heartbeat checks"
      >
        {padded.map((hb, idx) => {
          if (!hb) {
            return (
              <span
                key={`empty-${idx}`}
                class="uptime-bar-seg uptime-bar-seg--unknown"
                style={{ cursor: 'default' }}
                aria-hidden="true"
              />
            );
          }
          const info = heartbeatStatusInfo(hb.status);
          return (
            <button
              key={hb.id}
              type="button"
              class={`uptime-bar-seg uptime-bar-seg--${info.tone}`}
              aria-label={`${formatShortTime(hb.checked_at)}: ${info.label}, ${hb.latency_ms} ms`}
              aria-expanded={pinnedIdx === idx}
              onMouseEnter={() => {
                if (pinnedIdx === null) setHoverIdx(idx);
              }}
              onMouseLeave={() => {
                if (pinnedIdx === null) setHoverIdx(null);
              }}
              onClick={() => {
                setPinnedIdx((cur) => (cur === idx ? null : idx));
                setHoverIdx(null);
              }}
            />
          );
        })}
      </div>

      {activeHb ? (
        <div
          class={cls}
          style={style}
          role={pinnedIdx !== null ? 'dialog' : 'status'}
          aria-label={`Check details at ${formatShortTime(activeHb.checked_at)}`}
        >
          <div class="day-popover-head">
            <strong class="mono">{formatDateTime(activeHb.checked_at)}</strong>
            {pinnedIdx !== null ? (
              <button
                type="button"
                class="day-popover-close"
                aria-label="Close check details"
                onClick={() => setPinnedIdx(null)}
              >
                <CloseIcon size={14} />
              </button>
            ) : null}
          </div>
          <StatusBadge
            tone={heartbeatStatusInfo(activeHb.status).tone}
            label={heartbeatStatusInfo(activeHb.status).label}
            size={15}
          />
          <dl class="day-popover-stats">
            <dt>Latency</dt>
            <dd class="mono">{activeHb.latency_ms} ms</dd>
            <dt>Code</dt>
            <dd class="mono">{activeHb.status_code || '-'}</dd>
            <dt>Note</dt>
            <dd>{activeHb.message || 'OK'}</dd>
          </dl>
        </div>
      ) : null}

      <div class="uptime-bar-legend">
        <span class="mono">{checks.length > 0 ? formatShortTime(checks[0].checked_at) : 'Oldest'}</span>
        <span>One check every {intervalSec} s</span>
        <span class="mono">
          {checks.length > 0 ? formatShortTime(checks[checks.length - 1].checked_at) : 'Now'}
        </span>
      </div>
    </div>
  );
}

export function Sparkline30({ checks }: { checks: Heartbeat[] }) {
  const slots = 30;
  const recent = (checks || []).slice(-slots);
  const pad = Math.max(0, slots - recent.length);
  return (
    <div class="sparkline-30" aria-hidden="true">
      {Array.from({ length: pad }, (_, i) => (
        <span key={`p-${i}`} class="sparkline-seg sparkline-seg--unknown" />
      ))}
      {recent.map((hb) => {
        const tone = heartbeatStatusInfo(hb.status).tone;
        return <span key={hb.id} class={`sparkline-seg sparkline-seg--${tone}`} />;
      })}
    </div>
  );
}
