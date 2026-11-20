import type {
  ComponentStatus,
  HeartbeatStatus,
  IncidentImpact,
  IncidentStatus,
  MaintenanceStatus,
  OverallStatus,
} from '../types';

export type StatusTone = 'up' | 'degraded' | 'down' | 'maint' | 'unknown';

export function componentStatusInfo(status: ComponentStatus): { label: string; tone: StatusTone } {
  switch (status) {
    case 'operational':
      return { label: 'Operational', tone: 'up' };
    case 'degraded':
      return { label: 'Degraded Performance', tone: 'degraded' };
    case 'major_outage':
      return { label: 'Major Outage', tone: 'down' };
    case 'maintenance':
      return { label: 'Under Maintenance', tone: 'maint' };
    default:
      return { label: 'Pending Check', tone: 'unknown' };
  }
}

export function overallStatusInfo(status: OverallStatus): { label: string; tone: StatusTone } {
  switch (status) {
    case 'operational':
      return { label: 'All Systems Operational', tone: 'up' };
    case 'degraded':
      return { label: 'Degraded System Performance', tone: 'degraded' };
    case 'partial_outage':
      return { label: 'Partial System Outage', tone: 'degraded' };
    case 'major_outage':
      return { label: 'Major System Outage', tone: 'down' };
    case 'maintenance':
      return { label: 'Scheduled Maintenance in Progress', tone: 'maint' };
  }
}

export function heartbeatStatusInfo(status: HeartbeatStatus): { label: string; tone: StatusTone } {
  switch (status) {
    case 'up':
      return { label: 'Up', tone: 'up' };
    case 'degraded':
      return { label: 'Degraded', tone: 'degraded' };
    case 'down':
      return { label: 'Down', tone: 'down' };
  }
}

export function dayStatusInfo(status: string): { label: string; tone: StatusTone } {
  switch (status) {
    case 'up':
      return { label: 'Operational', tone: 'up' };
    case 'degraded':
      return { label: 'Degraded', tone: 'degraded' };
    case 'down':
      return { label: 'Outage', tone: 'down' };
    case 'maintenance':
      return { label: 'Maintenance', tone: 'maint' };
    default:
      return { label: 'No data', tone: 'unknown' };
  }
}

export function incidentStatusLabel(status: IncidentStatus): string {
  switch (status) {
    case 'investigating':
      return 'Investigating';
    case 'identified':
      return 'Identified';
    case 'monitoring':
      return 'Monitoring';
    case 'resolved':
      return 'Resolved';
  }
}

export function incidentImpactLabel(impact: IncidentImpact): string {
  switch (impact) {
    case 'none':
      return 'Informational';
    case 'minor':
      return 'Minor Impact';
    case 'major':
      return 'Major Impact';
    case 'critical':
      return 'Critical Impact';
  }
}

export function maintenanceStatusLabel(status: MaintenanceStatus): string {
  switch (status) {
    case 'scheduled':
      return 'Scheduled';
    case 'in_progress':
      return 'In Progress';
    case 'completed':
      return 'Completed';
  }
}

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return 'Not recorded';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function formatUTCTimestamp(iso: string | null | undefined): string {
  if (!iso) return 'Not recorded';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const mon = d.toLocaleString('en-US', { month: 'short', timeZone: 'UTC' });
  const day = d.getUTCDate();
  const hh = String(d.getUTCHours()).padStart(2, '0');
  const mm = String(d.getUTCMinutes()).padStart(2, '0');
  return `${mon} ${day}, ${hh}:${mm} UTC`;
}

export function formatClockHMS(iso: string | null | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const hh = String(d.getHours()).padStart(2, '0');
  const mm = String(d.getMinutes()).padStart(2, '0');
  const ss = String(d.getSeconds()).padStart(2, '0');
  return `${hh}:${mm}:${ss}`;
}

export function formatShortTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function formatDayLabel(dayStr: string): string {
  const parts = dayStr.split('-').map(Number);
  if (parts.length !== 3 || parts.some(Number.isNaN)) return dayStr;
  const d = new Date(Date.UTC(parts[0], parts[1] - 1, parts[2]));
  return d.toLocaleDateString(undefined, {
    timeZone: 'UTC',
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

export function formatDuration(seconds: number): string {
  if (seconds <= 0) return '0s';
  if (seconds < 60) return `${seconds}s`;
  const mins = Math.floor(seconds / 60);
  const remSec = seconds % 60;
  if (mins < 60) return remSec > 0 ? `${mins}m ${remSec}s` : `${mins}m`;
  const hrs = Math.floor(mins / 60);
  const remMins = mins % 60;
  if (hrs < 24) return remMins > 0 ? `${hrs}h ${remMins}m` : `${hrs}h`;
  const days = Math.floor(hrs / 24);
  const remHrs = hrs % 24;
  return remHrs > 0 ? `${days}d ${remHrs}h` : `${days}d`;
}

export function formatDurationSince(iso: string | null | undefined): string {
  if (!iso) return '';
  const ms = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '';
  return formatDuration(Math.floor(ms / 1000));
}

export function toLocalDateTimeInput(iso: string | null | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function slugify(input: string): string {
  return input
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

export function statusSortWeight(status: ComponentStatus): number {
  switch (status) {
    case 'major_outage':
      return 0;
    case 'degraded':
      return 1;
    case 'maintenance':
      return 2;
    case 'unknown':
      return 3;
    case 'operational':
      return 4;
  }
}
