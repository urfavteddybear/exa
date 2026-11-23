import type {
  AdminOverviewResponse,
  APIKey,
  APIKeyCreateResponse,
  BackupImportResponse,
  ComponentGroup,
  Incident,
  IncidentImpact,
  IncidentStatus,
  Maintenance,
  Monitor,
  MonitorDetailResponse,
  MonitorRange,
  MonitorType,
  NotificationChannel,
  NotificationType,
  PaginatedIncidentsResponse,
  ProbeResult,
  PublicStatusResponse,
  S3BackupObject,
  S3BackupResult,
  S3TestPayload,
  StatusPage,
  StatusPageMonitor,
  Subscriber,
  SubscriberType,
  User,
  UserRole,
} from './types';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && typeof init.body === 'string' && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin',
  });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    const msg =
      data && typeof data === 'object' && 'error' in data && typeof (data as { error: unknown }).error === 'string'
        ? (data as { error: string }).error
        : `Request failed with status ${res.status}`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  // Setup and auth
  setupStatus: () => request<{ setup_completed: boolean }>('/api/v1/setup/status'),
  setup: (body: { username: string; email: string; password: string; site_title: string }) =>
    request<{ user: User; status_page: StatusPage }>('/api/v1/setup', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  login: (body: { username: string; password: string }) =>
    request<{ user: User }>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  logout: () => request<{ ok: boolean }>('/api/v1/auth/logout', { method: 'POST' }),
  me: () => request<{ user: User }>('/api/v1/auth/me'),
  changePassword: (body: { current_password: string; new_password: string }) =>
    request<{ ok?: boolean }>('/api/v1/auth/password', {
      method: 'PUT',
      body: JSON.stringify(body),
    }),

  // Public status
  publicStatus: (slug?: string) =>
    request<PublicStatusResponse>(slug ? `/api/v1/status/${encodeURIComponent(slug)}` : '/api/v1/status'),
  publicIncidents: (slug: string, page = 1, limit = 20) =>
    request<PaginatedIncidentsResponse>(
      `/api/v1/status/${encodeURIComponent(slug)}/incidents?page=${page}&limit=${limit}`,
    ),
  subscribe: (slug: string, body: { type: SubscriberType; endpoint: string }) =>
    request<{ id: string; message: string }>(`/api/v1/status/${encodeURIComponent(slug)}/subscribe`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  // Admin Overview
  overview: () => request<AdminOverviewResponse>('/api/v1/overview'),

  // Monitors
  listMonitors: () => request<Monitor[]>('/api/v1/monitors'),
  getMonitor: (id: string, range: MonitorRange = '24h') =>
    request<MonitorDetailResponse>(
      `/api/v1/monitors/${encodeURIComponent(id)}?range=${encodeURIComponent(range)}`,
    ),
  createMonitor: (body: {
    name: string;
    slug?: string;
    type: MonitorType;
    target: string;
    group_id: string | null;
    interval_sec: number;
    timeout_sec: number;
    retries: number;
    degraded_ms: number;
    active?: boolean;
    sort_order?: number;
    config: Record<string, unknown>;
    notification_channel_ids?: string[];
    attach_default_page?: boolean;
    status_page_ids?: string[];
  }) =>
    request<Monitor>('/api/v1/monitors', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateMonitor: (
    id: string,
    body: {
      name: string;
      slug?: string;
      type: MonitorType;
      target: string;
      group_id: string | null;
      interval_sec: number;
      timeout_sec: number;
      retries: number;
      degraded_ms: number;
      active?: boolean;
      sort_order?: number;
      config: Record<string, unknown>;
      notification_channel_ids?: string[];
      status_page_ids?: string[];
    },
  ) =>
    request<Monitor>(`/api/v1/monitors/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteMonitor: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/monitors/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  pauseMonitor: (id: string) =>
    request<Monitor>(`/api/v1/monitors/${encodeURIComponent(id)}/pause`, { method: 'POST' }),
  resumeMonitor: (id: string) =>
    request<Monitor>(`/api/v1/monitors/${encodeURIComponent(id)}/resume`, { method: 'POST' }),
  testMonitor: (id: string) =>
    request<ProbeResult>(`/api/v1/monitors/${encodeURIComponent(id)}/test`, { method: 'POST' }),

  // Groups
  listGroups: () => request<ComponentGroup[]>('/api/v1/groups'),
  createGroup: (body: {
    name: string;
    description: string;
    sort_order: number;
    collapsed_default: boolean;
  }) =>
    request<ComponentGroup>('/api/v1/groups', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateGroup: (
    id: string,
    body: { name: string; description: string; sort_order: number; collapsed_default: boolean },
  ) =>
    request<ComponentGroup>(`/api/v1/groups/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteGroup: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/groups/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Incidents
  listIncidents: () => request<Incident[]>('/api/v1/incidents'),
  createIncident: (body: {
    status_page_id: string;
    title: string;
    impact: IncidentImpact;
    status: IncidentStatus;
    message: string;
    monitor_ids: string[];
  }) =>
    request<Incident>('/api/v1/incidents', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  addIncidentUpdate: (id: string, body: { status: IncidentStatus; message: string }) =>
    request<Incident>(`/api/v1/incidents/${encodeURIComponent(id)}/updates`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  deleteIncident: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/incidents/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Maintenances
  listMaintenances: () => request<Maintenance[]>('/api/v1/maintenances'),
  createMaintenance: (body: {
    status_page_id: string;
    title: string;
    description: string;
    scheduled_start: string;
    scheduled_end: string;
    auto_suppress_alerts: boolean;
    monitor_ids: string[];
  }) =>
    request<Maintenance>('/api/v1/maintenances', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateMaintenance: (
    id: string,
    body: {
      status_page_id: string;
      title: string;
      description: string;
      scheduled_start: string;
      scheduled_end: string;
      auto_suppress_alerts: boolean;
      monitor_ids: string[];
    },
  ) =>
    request<Maintenance>(`/api/v1/maintenances/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteMaintenance: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/maintenances/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Status Pages
  listStatusPages: () => request<StatusPage[]>('/api/v1/status-pages'),
  createStatusPage: (body: {
    slug: string;
    title: string;
    description: string;
    custom_css: string;
    is_default: boolean;
    is_public: boolean;
    show_latency: boolean;
  }) =>
    request<StatusPage>('/api/v1/status-pages', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateStatusPage: (
    id: string,
    body: {
      slug: string;
      title: string;
      description: string;
      custom_css: string;
      is_default: boolean;
      is_public: boolean;
      show_latency: boolean;
    },
  ) =>
    request<StatusPage>(`/api/v1/status-pages/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteStatusPage: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/status-pages/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  listStatusPageMonitors: (id: string) =>
    request<StatusPageMonitor[]>(`/api/v1/status-pages/${encodeURIComponent(id)}/monitors`),
  setStatusPageMonitors: (id: string, monitors: StatusPageMonitor[]) =>
    request<{ ok?: boolean }>(`/api/v1/status-pages/${encodeURIComponent(id)}/monitors`, {
      method: 'PUT',
      body: JSON.stringify({ monitors }),
    }),

  // Notifications
  listNotifications: () => request<NotificationChannel[]>('/api/v1/notifications'),
  createNotification: (body: {
    name: string;
    type: NotificationType;
    config: Record<string, unknown>;
    is_default: boolean;
    enabled: boolean;
  }) =>
    request<NotificationChannel>('/api/v1/notifications', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateNotification: (
    id: string,
    body: {
      name: string;
      type: NotificationType;
      config: Record<string, unknown>;
      is_default: boolean;
      enabled: boolean;
    },
  ) =>
    request<NotificationChannel>(`/api/v1/notifications/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteNotification: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/notifications/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  testNotification: (id: string) =>
    request<{ ok?: boolean; message?: string }>(`/api/v1/notifications/${encodeURIComponent(id)}/test`, {
      method: 'POST',
    }),

  // Subscribers
  listSubscribers: () => request<Subscriber[]>('/api/v1/subscribers'),
  deleteSubscriber: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/subscribers/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // API Keys
  listApiKeys: () => request<APIKey[]>('/api/v1/api-keys'),
  createApiKey: (body: { name: string; role: UserRole }) =>
    request<APIKeyCreateResponse>('/api/v1/api-keys', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  deleteApiKey: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/api-keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Users
  listUsers: () => request<User[]>('/api/v1/users'),
  createUser: (body: { username: string; email: string; password: string; role: UserRole }) =>
    request<User>('/api/v1/users', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  deleteUser: (id: string) =>
    request<{ ok?: boolean }>(`/api/v1/users/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Settings & Backup
  getSettings: () => request<Record<string, string>>('/api/v1/settings'),
  updateSettings: (body: Record<string, string>) =>
    request<Record<string, string>>('/api/v1/settings', {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  exportBackupBlob: async (gzip: boolean): Promise<Blob> => {
    const url = gzip ? '/api/v1/backup/export?format=gzip' : '/api/v1/backup/export';
    const res = await fetch(url, { credentials: 'same-origin' });
    if (!res.ok) {
      let msg = `Export failed with status ${res.status}`;
      try {
        const data = (await res.json()) as { error?: string };
        if (data?.error) msg = data.error;
      } catch {
        // Ignore JSON parse errors on error body.
      }
      throw new ApiError(res.status, msg);
    }
    return res.blob();
  },
  importBackupFile: async (file: File): Promise<BackupImportResponse> => {
    const isGzip =
      file.name.toLowerCase().endsWith('.gz') ||
      file.type === 'application/gzip' ||
      file.type === 'application/x-gzip';
    return request<BackupImportResponse>('/api/v1/backup/import', {
      method: 'POST',
      headers: { 'Content-Type': isGzip ? 'application/gzip' : 'application/json' },
      body: file,
    });
  },
  testS3Backup: (payload?: S3TestPayload) =>
    request<{ ok: boolean }>('/api/v1/backup/s3/test', {
      method: 'POST',
      body: payload ? JSON.stringify(payload) : undefined,
    }),
  runS3Backup: () =>
    request<S3BackupResult>('/api/v1/backup/s3/run', {
      method: 'POST',
    }),
  listS3Backups: () => request<S3BackupObject[]>('/api/v1/backup/s3/list'),
  restoreS3Backup: (key: string) =>
    request<BackupImportResponse>('/api/v1/backup/s3/restore', {
      method: 'POST',
      body: JSON.stringify({ key }),
    }),
  deleteS3Backup: (key: string) =>
    request<{ ok: boolean }>(`/api/v1/backup/s3/object?key=${encodeURIComponent(key)}`, {
      method: 'DELETE',
    }),
};

export function subscribeStatusStream(slug: string, onEvent: (type: string) => void): () => void {
  const url = slug ? `/api/v1/stream?slug=${encodeURIComponent(slug)}` : '/api/v1/stream';
  const events = ['heartbeat', 'status_change', 'incident_update', 'maintenance_update'];
  const handler = (ev: MessageEvent) => {
    onEvent(ev.type);
  };
  let es: EventSource;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let delay = 2000;
  let dropped = false;
  const connect = () => {
    es = new EventSource(url);
    for (const name of events) {
      es.addEventListener(name, handler);
    }
    es.onopen = () => {
      delay = 2000;
      if (dropped) {
        dropped = false;
        onEvent('reconnect');
      }
    };
    // EventSource retries transient drops itself, but gives up for good after a
    // non-200 response (429, 502 during restart), so reconnect manually then.
    es.onerror = () => {
      dropped = true;
      if (es.readyState !== EventSource.CLOSED) return;
      timer = setTimeout(connect, delay);
      delay = Math.min(delay * 2, 60000);
    };
  };
  connect();
  return () => {
    clearTimeout(timer);
    for (const name of events) {
      es.removeEventListener(name, handler);
    }
    es.close();
  };
}
