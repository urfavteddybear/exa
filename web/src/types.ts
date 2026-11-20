export type MonitorType = 'http' | 'tcp' | 'ping' | 'dns' | 'push';

export type ComponentStatus =
  | 'operational'
  | 'degraded'
  | 'major_outage'
  | 'maintenance'
  | 'unknown';

export type HeartbeatStatus = 'up' | 'degraded' | 'down';

export type OverallStatus =
  | 'operational'
  | 'degraded'
  | 'partial_outage'
  | 'major_outage'
  | 'maintenance';

export type IncidentImpact = 'none' | 'minor' | 'major' | 'critical';
export type IncidentStatus = 'investigating' | 'identified' | 'monitoring' | 'resolved';
export type MaintenanceStatus = 'scheduled' | 'in_progress' | 'completed';
export type NotificationType = 'webhook' | 'discord' | 'slack' | 'telegram' | 'email' | 'ntfy';
export type UserRole = 'admin' | 'viewer';
export type SubscriberType = 'email' | 'webhook';
export type MonitorRange = '1h' | '24h' | '7d' | '30d';

export interface HTTPConfig {
  method?: string;
  expected_status?: string;
  keyword?: string;
  keyword_invert?: boolean;
  json_path?: string;
  json_expected?: string;
  headers?: Record<string, string>;
  body?: string;
  follow_redirects?: boolean;
  ignore_tls?: boolean;
  tls_expiry_warn_days?: number;
}

export interface TCPConfig {
  send_data?: string;
  expect_data?: string;
}

export interface PingConfig {
  packet_count?: number;
}

export interface DNSConfig {
  record_type?: string;
  resolver?: string;
  expected_value?: string;
}

export interface PushConfig {
  grace_sec?: number;
}

export interface User {
  id: string;
  username: string;
  email: string;
  role: UserRole;
  created_at: string;
}

export interface APIKey {
  id: string;
  name: string;
  key_prefix: string;
  role: UserRole;
  last_used_at: string | null;
  created_at: string;
}

export interface APIKeyCreateResponse extends APIKey {
  token: string;
}

export interface ComponentGroup {
  id: string;
  name: string;
  description: string;
  sort_order: number;
  collapsed_default: boolean;
  created_at: string;
}

export interface Monitor {
  id: string;
  name: string;
  slug: string;
  type: MonitorType;
  target: string;
  group_id: string | null;
  interval_sec: number;
  timeout_sec: number;
  retries: number;
  degraded_ms: number;
  active: boolean;
  sort_order: number;
  config: Record<string, unknown> | null;
  push_token?: string;
  current_status: ComponentStatus;
  last_checked_at: string | null;
  last_latency_ms: number | null;
  tls_expires_at: string | null;
  tls_days_left: number | null;
  last_error: string;
  created_at: string;
  updated_at: string;
}

export interface Heartbeat {
  id: number;
  monitor_id: string;
  status: HeartbeatStatus;
  latency_ms: number;
  status_code: number;
  message: string;
  checked_at: string;
}

export interface DailyRollup {
  monitor_id: string;
  day: string;
  total_checks: number;
  up_checks: number;
  degraded_checks: number;
  down_checks: number;
  avg_latency_ms: number;
  min_latency_ms: number;
  max_latency_ms: number;
  downtime_sec: number;
}

export interface MonitorDetailResponse extends Monitor {
  heartbeats: Heartbeat[];
  daily_rollups: DailyRollup[];
  notification_channel_ids?: string[];
  status_page_ids?: string[];
  uptime_24h?: number | null;
  uptime_30d?: number | null;
  uptime_90d?: number | null;
  avg_latency_24h?: number | null;
  status_since?: string | null;
  recent_checks_60?: Heartbeat[];
  incidents?: Incident[];
  channel_names?: string[];
  status_page_names?: string[];
  range?: string;
}

export interface OverviewBucket {
  timestamp: string;
  status: 'up' | 'degraded' | 'down' | 'no_data';
  uptime_pct: number | null;
  total_checks: number;
  down_checks: number;
}

export interface ResponsePoint {
  timestamp: string;
  median_ms: number | null;
}

export interface OverviewAlert {
  monitor_id: string;
  monitor_name: string;
  target: string;
  current_status: ComponentStatus;
  last_error: string;
  since: string | null;
  incident_id: string;
}

export interface OverviewMonitor extends Monitor {
  group_name: string;
  uptime_24h: number | null;
  uptime_30d: number | null;
  recent_checks: Heartbeat[];
}

export interface OverviewActivity {
  timestamp: string;
  tone: 'up' | 'degraded' | 'down' | 'maint' | 'neutral';
  title: string;
  detail: string;
  monitor_id?: string;
  incident_id?: string;
}

export interface TLSCertSummary {
  monitor_id: string;
  name: string;
  target: string;
  tls_days_left: number;
  tls_expires_at: string | null;
  warn_days: number;
}

export interface AdminOverviewResponse {
  refreshed_at: string;
  total_monitors: number;
  operational_monitors: number;
  degraded_monitors: number;
  down_monitors: number;
  paused_monitors: number;
  maintenance_monitors: number;
  uptime_30d: number | null;
  downtime_30d_sec: number;
  median_response_24h_ms: number | null;
  median_response_prev_24h_ms: number | null;
  open_incidents_count: number;
  availability_24h_pct: number | null;
  availability_24h: OverviewBucket[];
  response_series_24h: ResponsePoint[];
  active_alerts: OverviewAlert[];
  monitors: OverviewMonitor[];
  activity: OverviewActivity[];
  tls_certificates: TLSCertSummary[];
  default_page_slug: string;
  default_page_title: string;
  base_url: string;
}

export interface ProbeResult {
  status: HeartbeatStatus;
  latency_ms: number;
  status_code: number;
  message: string;
  tls_expires_at: string | null;
  tls_days_left: number | null;
  checked_at: string;
}

export interface StatusPage {
  id: string;
  slug: string;
  title: string;
  description: string;
  custom_css: string;
  is_default: boolean;
  is_public: boolean;
  show_latency: boolean;
  created_at: string;
  updated_at: string;
}

export interface StatusPageMonitor {
  status_page_id: string;
  monitor_id: string;
  group_id: string | null;
  display_name: string;
  sort_order: number;
}

export interface IncidentUpdate {
  id: string;
  incident_id: string;
  status: IncidentStatus;
  message: string;
  created_at: string;
}

export interface Incident {
  id: string;
  status_page_id: string;
  title: string;
  impact: IncidentImpact;
  status: IncidentStatus;
  created_at: string;
  resolved_at: string | null;
  updated_at: string;
  updates?: IncidentUpdate[];
  monitor_ids?: string[];
}

export interface Maintenance {
  id: string;
  status_page_id: string;
  title: string;
  description: string;
  status: MaintenanceStatus;
  scheduled_start: string;
  scheduled_end: string;
  auto_suppress_alerts: boolean;
  created_at: string;
  monitor_ids?: string[];
}

export interface NotificationChannel {
  id: string;
  name: string;
  type: NotificationType;
  config: Record<string, unknown>;
  is_default: boolean;
  enabled: boolean;
  created_at: string;
}

export interface Subscriber {
  id: string;
  status_page_id: string;
  type: SubscriberType;
  endpoint: string;
  verified: boolean;
  active: boolean;
  created_at: string;
}

export interface IncidentBrief {
  id: string;
  title: string;
  impact: IncidentImpact;
}

export interface PublicDay {
  day: string;
  status: 'up' | 'degraded' | 'down' | 'maintenance' | 'no_data';
  uptime_pct: number | null;
  total_checks: number;
  down_checks: number;
  downtime_sec: number;
  avg_latency_ms: number;
  incidents: IncidentBrief[];
}

export interface PublicMonitor {
  id: string;
  name: string;
  slug: string;
  type: MonitorType;
  current_status: ComponentStatus;
  last_latency_ms: number | null;
  last_checked_at: string | null;
  tls_days_left: number | null;
  uptime_90d: number | null;
  days: PublicDay[];
}

export interface PublicGroup {
  id: string;
  name: string;
  description: string;
  collapsed_default: boolean;
  monitors: PublicMonitor[];
}

export interface PageSummary {
  slug: string;
  title: string;
}

export interface PublicStatusResponse {
  page: StatusPage;
  overall_status: OverallStatus;
  overall_label: string;
  updated_at: string;
  subscriptions_enabled?: boolean;
  groups: PublicGroup[];
  ungrouped_monitors: PublicMonitor[];
  active_incidents: Incident[];
  active_maintenances: Maintenance[];
  upcoming_maintenances: Maintenance[];
  past_incidents: Incident[];
  other_pages: PageSummary[];
}

export interface PaginatedIncidentsResponse {
  incidents: Incident[];
  total: number;
  page: number;
  limit: number;
}
