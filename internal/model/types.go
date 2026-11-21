package model

import (
	"context"
	"encoding/json"
	"time"
)

type MonitorType string

const (
	MonitorTypeHTTP MonitorType = "http"
	MonitorTypeTCP  MonitorType = "tcp"
	MonitorTypePing MonitorType = "ping"
	MonitorTypeDNS  MonitorType = "dns"
	MonitorTypePush MonitorType = "push"
)

type ComponentStatus string

const (
	ComponentStatusOperational ComponentStatus = "operational"
	ComponentStatusDegraded    ComponentStatus = "degraded"
	ComponentStatusMajorOutage ComponentStatus = "major_outage"
	ComponentStatusMaintenance ComponentStatus = "maintenance"
	ComponentStatusUnknown     ComponentStatus = "unknown"
)

type HeartbeatStatus string

const (
	HeartbeatStatusUp       HeartbeatStatus = "up"
	HeartbeatStatusDegraded HeartbeatStatus = "degraded"
	HeartbeatStatusDown     HeartbeatStatus = "down"
)

type OverallStatus string

const (
	OverallStatusOperational   OverallStatus = "operational"
	OverallStatusDegraded      OverallStatus = "degraded"
	OverallStatusPartialOutage OverallStatus = "partial_outage"
	OverallStatusMajorOutage   OverallStatus = "major_outage"
	OverallStatusMaintenance   OverallStatus = "maintenance"
)

type IncidentImpact string

const (
	IncidentImpactNone     IncidentImpact = "none"
	IncidentImpactMinor    IncidentImpact = "minor"
	IncidentImpactMajor    IncidentImpact = "major"
	IncidentImpactCritical IncidentImpact = "critical"
)

type IncidentStatus string

const (
	IncidentStatusInvestigating IncidentStatus = "investigating"
	IncidentStatusIdentified    IncidentStatus = "identified"
	IncidentStatusMonitoring    IncidentStatus = "monitoring"
	IncidentStatusResolved      IncidentStatus = "resolved"
)

type MaintenanceStatus string

const (
	MaintenanceStatusScheduled  MaintenanceStatus = "scheduled"
	MaintenanceStatusInProgress MaintenanceStatus = "in_progress"
	MaintenanceStatusCompleted  MaintenanceStatus = "completed"
)

type NotificationType string

const (
	NotificationTypeWebhook  NotificationType = "webhook"
	NotificationTypeDiscord  NotificationType = "discord"
	NotificationTypeSlack    NotificationType = "slack"
	NotificationTypeTelegram NotificationType = "telegram"
	NotificationTypeEmail    NotificationType = "email"
	NotificationTypeNtfy     NotificationType = "ntfy"
)

type UserRole string

const (
	UserRoleAdmin  UserRole = "admin"
	UserRoleViewer UserRole = "viewer"
)

type SubscriberType string

const (
	SubscriberTypeEmail   SubscriberType = "email"
	SubscriberTypeWebhook SubscriberType = "webhook"
)

// Probe configuration structs.

type HTTPConfig struct {
	Method            string            `json:"method,omitempty"`
	ExpectedStatus    string            `json:"expected_status,omitempty"`
	Keyword           string            `json:"keyword,omitempty"`
	KeywordInvert     bool              `json:"keyword_invert,omitempty"`
	JSONPath          string            `json:"json_path,omitempty"`
	JSONExpected      string            `json:"json_expected,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	Body              string            `json:"body,omitempty"`
	FollowRedirects   *bool             `json:"follow_redirects,omitempty"`
	IgnoreTLS         bool              `json:"ignore_tls,omitempty"`
	TLSExpiryWarnDays int               `json:"tls_expiry_warn_days,omitempty"`
}

func (c HTTPConfig) WithDefaults() HTTPConfig {
	if c.Method == "" {
		c.Method = "GET"
	}
	if c.ExpectedStatus == "" {
		c.ExpectedStatus = "200-299"
	}
	if c.FollowRedirects == nil {
		t := true
		c.FollowRedirects = &t
	}
	if c.TLSExpiryWarnDays <= 0 {
		c.TLSExpiryWarnDays = 14
	}
	return c
}

type TCPConfig struct {
	SendData   string `json:"send_data,omitempty"`
	ExpectData string `json:"expect_data,omitempty"`
}

type PingConfig struct {
	PacketCount int `json:"packet_count,omitempty"`
}

func (c PingConfig) WithDefaults() PingConfig {
	if c.PacketCount <= 0 {
		c.PacketCount = 2
	}
	return c
}

type DNSConfig struct {
	RecordType    string `json:"record_type,omitempty"`
	Resolver      string `json:"resolver,omitempty"`
	ExpectedValue string `json:"expected_value,omitempty"`
}

func (c DNSConfig) WithDefaults() DNSConfig {
	if c.RecordType == "" {
		c.RecordType = "A"
	}
	return c
}

type PushConfig struct {
	GraceSec int `json:"grace_sec,omitempty"`
}

func (c PushConfig) WithDefaults() PushConfig {
	if c.GraceSec <= 0 {
		c.GraceSec = 60
	}
	return c
}

// Notification configuration structs.

type WebhookConfig struct {
	URL     string            `json:"url"`
	Secret  string            `json:"secret,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type DiscordConfig struct {
	WebhookURL string `json:"webhook_url"`
	Username   string `json:"username,omitempty"`
}

type SlackConfig struct {
	WebhookURL string `json:"webhook_url"`
	Channel    string `json:"channel,omitempty"`
}

type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type EmailConfig struct {
	SMTPHost string   `json:"smtp_host"`
	SMTPPort int      `json:"smtp_port"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	UseTLS   bool     `json:"use_tls,omitempty"`
}

type NtfyConfig struct {
	ServerURL string `json:"server_url,omitempty"`
	Topic     string `json:"topic"`
	Token     string `json:"token,omitempty"`
	Priority  string `json:"priority,omitempty"`
}

func (c NtfyConfig) WithDefaults() NtfyConfig {
	if c.ServerURL == "" {
		c.ServerURL = "https://ntfy.sh"
	}
	return c
}

// Core domain entities.

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         UserRole  `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	KeyHash    string     `json:"-"`
	Role       UserRole   `json:"role"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type ComponentGroup struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	SortOrder        int       `json:"sort_order"`
	CollapsedDefault bool      `json:"collapsed_default"`
	CreatedAt        time.Time `json:"created_at"`
}

type Monitor struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Slug          string          `json:"slug"`
	Type          MonitorType     `json:"type"`
	Target        string          `json:"target"`
	GroupID       *string         `json:"group_id"`
	IntervalSec   int             `json:"interval_sec"`
	TimeoutSec    int             `json:"timeout_sec"`
	Retries       int             `json:"retries"`
	DegradedMs    int             `json:"degraded_ms"`
	Active        bool            `json:"active"`
	SortOrder     int             `json:"sort_order"`
	Config        json.RawMessage `json:"config"`
	PushToken     string          `json:"push_token,omitempty"`
	CurrentStatus ComponentStatus `json:"current_status"`
	LastCheckedAt *time.Time      `json:"last_checked_at"`
	LastLatencyMs *int            `json:"last_latency_ms"`
	TLSExpiresAt  *time.Time      `json:"tls_expires_at"`
	TLSDaysLeft   *int            `json:"tls_days_left"`
	LastError     string          `json:"last_error"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Heartbeat struct {
	ID         int64           `json:"id"`
	MonitorID  string          `json:"monitor_id"`
	Status     HeartbeatStatus `json:"status"`
	LatencyMs  int             `json:"latency_ms"`
	StatusCode int             `json:"status_code"`
	Message    string          `json:"message"`
	CheckedAt  time.Time       `json:"checked_at"`
}

type DailyRollup struct {
	MonitorID      string  `json:"monitor_id"`
	Day            string  `json:"day"`
	TotalChecks    int     `json:"total_checks"`
	UpChecks       int     `json:"up_checks"`
	DegradedChecks int     `json:"degraded_checks"`
	DownChecks     int     `json:"down_checks"`
	SumLatencyMs   int64   `json:"-"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	MinLatencyMs   int     `json:"min_latency_ms"`
	MaxLatencyMs   int     `json:"max_latency_ms"`
	DowntimeSec    int     `json:"downtime_sec"`
}

type StatusPage struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CustomCSS   string    `json:"custom_css"`
	IsDefault   bool      `json:"is_default"`
	IsPublic    bool      `json:"is_public"`
	ShowLatency bool      `json:"show_latency"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type StatusPageMonitor struct {
	StatusPageID string  `json:"status_page_id"`
	MonitorID    string  `json:"monitor_id"`
	GroupID      *string `json:"group_id"`
	DisplayName  string  `json:"display_name"`
	SortOrder    int     `json:"sort_order"`
}

type Incident struct {
	ID           string           `json:"id"`
	StatusPageID string           `json:"status_page_id"`
	Title        string           `json:"title"`
	Impact       IncidentImpact   `json:"impact"`
	Status       IncidentStatus   `json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
	ResolvedAt   *time.Time       `json:"resolved_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Updates      []IncidentUpdate `json:"updates,omitempty"`
	MonitorIDs   []string         `json:"monitor_ids,omitempty"`
}

type IncidentUpdate struct {
	ID         string         `json:"id"`
	IncidentID string         `json:"incident_id"`
	Status     IncidentStatus `json:"status"`
	Message    string         `json:"message"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Maintenance struct {
	ID                 string            `json:"id"`
	StatusPageID       string            `json:"status_page_id"`
	Title              string            `json:"title"`
	Description        string            `json:"description"`
	Status             MaintenanceStatus `json:"status"`
	ScheduledStart     time.Time         `json:"scheduled_start"`
	ScheduledEnd       time.Time         `json:"scheduled_end"`
	AutoSuppressAlerts bool              `json:"auto_suppress_alerts"`
	CreatedAt          time.Time         `json:"created_at"`
	MonitorIDs         []string          `json:"monitor_ids,omitempty"`
}

type NotificationChannel struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Type      NotificationType `json:"type"`
	Config    json.RawMessage  `json:"config"`
	IsDefault bool             `json:"is_default"`
	Enabled   bool             `json:"enabled"`
	CreatedAt time.Time        `json:"created_at"`
}

type MonitorNotification struct {
	MonitorID string `json:"monitor_id"`
	ChannelID string `json:"channel_id"`
}

type Subscriber struct {
	ID           string         `json:"id"`
	StatusPageID string         `json:"status_page_id"`
	Type         SubscriberType `json:"type"`
	Endpoint     string         `json:"endpoint"`
	Token        string         `json:"-"`
	Verified     bool           `json:"verified"`
	Active       bool           `json:"active"`
	CreatedAt    time.Time      `json:"created_at"`
}

type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Probe and runtime contracts.

type ProbeResult struct {
	Status       HeartbeatStatus `json:"status"`
	LatencyMs    int             `json:"latency_ms"`
	StatusCode   int             `json:"status_code"`
	Message      string          `json:"message"`
	TLSExpiresAt *time.Time      `json:"tls_expires_at"`
	TLSDaysLeft  *int            `json:"tls_days_left"`
	CheckedAt    time.Time       `json:"checked_at"`
}

type SSEEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type EventBroadcaster interface {
	Broadcast(ev SSEEvent)
}

type MonitorController interface {
	ReloadMonitor(id string)
	RemoveMonitor(id string)
	ReloadAll()
	TestMonitorNow(ctx context.Context, m Monitor) ProbeResult
	HandlePush(ctx context.Context, token string, status HeartbeatStatus, latencyMs int, msg string) (*Monitor, error)
}

type Notifier interface {
	NotifyMonitorStateChange(ctx context.Context, m Monitor, prev, next ComponentStatus, errMsg string)
	NotifyIncidentUpdate(ctx context.Context, page StatusPage, inc Incident, upd IncidentUpdate)
	NotifyMaintenanceUpdate(ctx context.Context, page StatusPage, maint Maintenance)
	TestChannel(ctx context.Context, ch NotificationChannel) error
}

// Public status API and admin payloads.

type IncidentBrief struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Impact IncidentImpact `json:"impact"`
}

type PublicDay struct {
	Day          string          `json:"day"`
	Status       string          `json:"status"`
	UptimePct    *float64        `json:"uptime_pct"`
	TotalChecks  int             `json:"total_checks"`
	DownChecks   int             `json:"down_checks"`
	DowntimeSec  int             `json:"downtime_sec"`
	AvgLatencyMs float64         `json:"avg_latency_ms"`
	Incidents    []IncidentBrief `json:"incidents"`
}

type PublicMonitor struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Slug          string          `json:"slug"`
	Type          MonitorType     `json:"type"`
	CurrentStatus ComponentStatus `json:"current_status"`
	LastLatencyMs *int            `json:"last_latency_ms"`
	LastCheckedAt *time.Time      `json:"last_checked_at"`
	TLSDaysLeft   *int            `json:"tls_days_left"`
	Uptime90d     *float64        `json:"uptime_90d"`
	Days          []PublicDay     `json:"days"`
}

type PublicGroup struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	CollapsedDefault bool            `json:"collapsed_default"`
	Monitors         []PublicMonitor `json:"monitors"`
}

type PageSummary struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type PublicStatusResponse struct {
	Page                 StatusPage      `json:"page"`
	OverallStatus        OverallStatus   `json:"overall_status"`
	OverallLabel         string          `json:"overall_label"`
	UpdatedAt            time.Time       `json:"updated_at"`
	Groups               []PublicGroup   `json:"groups"`
	UngroupedMonitors    []PublicMonitor `json:"ungrouped_monitors"`
	ActiveIncidents      []Incident      `json:"active_incidents"`
	ActiveMaintenances   []Maintenance   `json:"active_maintenances"`
	UpcomingMaintenances []Maintenance   `json:"upcoming_maintenances"`
	PastIncidents        []Incident      `json:"past_incidents"`
	OtherPages           []PageSummary   `json:"other_pages"`
	SubscriptionsEnabled bool            `json:"subscriptions_enabled"`
}

type MonitorDetailResponse struct {
	Monitor
	Heartbeats             []Heartbeat   `json:"heartbeats"`
	DailyRollups           []DailyRollup `json:"daily_rollups"`
	NotificationChannelIDs []string      `json:"notification_channel_ids"`
	StatusPageIDs          []string      `json:"status_page_ids"`
	Uptime24h              *float64      `json:"uptime_24h"`
	Uptime30d              *float64      `json:"uptime_30d"`
	Uptime90d              *float64      `json:"uptime_90d"`
	AvgLatency24h          *int          `json:"avg_latency_24h"`
	StatusSince            *time.Time    `json:"status_since"`
	RecentChecks60         []Heartbeat   `json:"recent_checks_60"`
	Incidents              []Incident    `json:"incidents"`
	ChannelNames           []string      `json:"channel_names"`
	StatusPageNames        []string      `json:"status_page_names"`
	Range                  string        `json:"range"`
}

// Admin overview DTOs.

type OverviewBucket struct {
	Timestamp   time.Time `json:"timestamp"`
	Status      string    `json:"status"`
	UptimePct   *float64  `json:"uptime_pct"`
	TotalChecks int       `json:"total_checks"`
	DownChecks  int       `json:"down_checks"`
}

type ResponsePoint struct {
	Timestamp time.Time `json:"timestamp"`
	MedianMs  *int      `json:"median_ms"`
}

type OverviewAlert struct {
	MonitorID     string          `json:"monitor_id"`
	MonitorName   string          `json:"monitor_name"`
	Target        string          `json:"target"`
	CurrentStatus ComponentStatus `json:"current_status"`
	LastError     string          `json:"last_error"`
	Since         *time.Time      `json:"since"`
	IncidentID    string          `json:"incident_id"`
}

type OverviewMonitor struct {
	Monitor
	GroupName    string      `json:"group_name"`
	Uptime24h    *float64    `json:"uptime_24h"`
	Uptime30d    *float64    `json:"uptime_30d"`
	RecentChecks []Heartbeat `json:"recent_checks"`
}

type OverviewActivity struct {
	Timestamp  time.Time `json:"timestamp"`
	Tone       string    `json:"tone"`
	Title      string    `json:"title"`
	Detail     string    `json:"detail"`
	MonitorID  string    `json:"monitor_id,omitempty"`
	IncidentID string    `json:"incident_id,omitempty"`
}

type TLSCertSummary struct {
	MonitorID    string     `json:"monitor_id"`
	Name         string     `json:"name"`
	Target       string     `json:"target"`
	TLSDaysLeft  int        `json:"tls_days_left"`
	TLSExpiresAt *time.Time `json:"tls_expires_at"`
	WarnDays     int        `json:"warn_days"`
}

type AdminOverviewResponse struct {
	RefreshedAt             time.Time          `json:"refreshed_at"`
	TotalMonitors           int                `json:"total_monitors"`
	OperationalMonitors     int                `json:"operational_monitors"`
	DegradedMonitors        int                `json:"degraded_monitors"`
	DownMonitors            int                `json:"down_monitors"`
	PausedMonitors          int                `json:"paused_monitors"`
	MaintenanceMonitors     int                `json:"maintenance_monitors"`
	Uptime30d               *float64           `json:"uptime_30d"`
	Downtime30dSec          int                `json:"downtime_30d_sec"`
	MedianResponse24hMs     *int               `json:"median_response_24h_ms"`
	MedianResponsePrev24hMs *int               `json:"median_response_prev_24h_ms"`
	OpenIncidentsCount      int                `json:"open_incidents_count"`
	Availability24hPct      *float64           `json:"availability_24h_pct"`
	Availability24h         []OverviewBucket   `json:"availability_24h"`
	ResponseSeries24h       []ResponsePoint    `json:"response_series_24h"`
	ActiveAlerts            []OverviewAlert    `json:"active_alerts"`
	Monitors                []OverviewMonitor  `json:"monitors"`
	Activity                []OverviewActivity `json:"activity"`
	TLSCertificates         []TLSCertSummary   `json:"tls_certificates"`
	DefaultPageSlug         string             `json:"default_page_slug"`
	DefaultPageTitle        string             `json:"default_page_title"`
	BaseURL                 string             `json:"base_url"`
}

type SetupRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	SiteTitle string `json:"site_title"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type MonitorUpsertRequest struct {
	Name                   string          `json:"name"`
	Slug                   string          `json:"slug"`
	Type                   MonitorType     `json:"type"`
	Target                 string          `json:"target"`
	GroupID                *string         `json:"group_id"`
	IntervalSec            int             `json:"interval_sec"`
	TimeoutSec             int             `json:"timeout_sec"`
	Retries                int             `json:"retries"`
	DegradedMs             int             `json:"degraded_ms"`
	Active                 *bool           `json:"active"`
	SortOrder              int             `json:"sort_order"`
	Config                 json.RawMessage `json:"config"`
	ConfigJSON             string          `json:"config_json"`
	NotificationChannelIDs []string        `json:"notification_channel_ids"`
	AttachDefaultPage      *bool           `json:"attach_default_page"`
	StatusPageIDs          []string        `json:"status_page_ids"`
}

type GroupUpsertRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	SortOrder        int    `json:"sort_order"`
	CollapsedDefault bool   `json:"collapsed_default"`
}

type IncidentCreateRequest struct {
	StatusPageID string         `json:"status_page_id"`
	Title        string         `json:"title"`
	Impact       IncidentImpact `json:"impact"`
	Status       IncidentStatus `json:"status"`
	Message      string         `json:"message"`
	MonitorIDs   []string       `json:"monitor_ids"`
}

type IncidentUpdateRequest struct {
	Status  IncidentStatus `json:"status"`
	Message string         `json:"message"`
}

type MaintenanceUpsertRequest struct {
	StatusPageID       string    `json:"status_page_id"`
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	ScheduledStart     time.Time `json:"scheduled_start"`
	ScheduledEnd       time.Time `json:"scheduled_end"`
	AutoSuppressAlerts *bool     `json:"auto_suppress_alerts"`
	MonitorIDs         []string  `json:"monitor_ids"`
}

type StatusPageUpsertRequest struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	CustomCSS   string `json:"custom_css"`
	IsDefault   bool   `json:"is_default"`
	IsPublic    *bool  `json:"is_public"`
	ShowLatency *bool  `json:"show_latency"`
}

type SetPageMonitorsRequest struct {
	Monitors []StatusPageMonitor `json:"monitors"`
}

type NotificationUpsertRequest struct {
	Name      string           `json:"name"`
	Type      NotificationType `json:"type"`
	Config    json.RawMessage  `json:"config"`
	IsDefault bool             `json:"is_default"`
	Enabled   *bool            `json:"enabled"`
}

type SubscribeRequest struct {
	Type     SubscriberType `json:"type"`
	Endpoint string         `json:"endpoint"`
}

type APIKeyCreateRequest struct {
	Name string   `json:"name"`
	Role UserRole `json:"role"`
}

type APIKeyCreateResponse struct {
	APIKey
	Token string `json:"token"`
}

type UserCreateRequest struct {
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Role     UserRole `json:"role"`
}

type BackupDailyRollup struct {
	MonitorID      string `json:"monitor_id"`
	Day            string `json:"day"`
	TotalChecks    int    `json:"total_checks"`
	UpChecks       int    `json:"up_checks"`
	DegradedChecks int    `json:"degraded_checks"`
	DownChecks     int    `json:"down_checks"`
	SumLatencyMs   int64  `json:"sum_latency_ms"`
	MinLatencyMs   int    `json:"min_latency_ms"`
	MaxLatencyMs   int    `json:"max_latency_ms"`
	DowntimeSec    int    `json:"downtime_sec"`
}

type BackupData struct {
	Version              int                   `json:"version"`
	ExportedAt           time.Time             `json:"exported_at"`
	Settings             map[string]string     `json:"settings"`
	ComponentGroups      []ComponentGroup      `json:"component_groups"`
	Monitors             []Monitor             `json:"monitors"`
	Heartbeats           []Heartbeat           `json:"heartbeats,omitempty"`
	DailyRollups         []BackupDailyRollup   `json:"daily_rollups,omitempty"`
	StatusPages          []StatusPage          `json:"status_pages"`
	StatusPageMonitors   []StatusPageMonitor   `json:"status_page_monitors"`
	NotificationChannels []NotificationChannel `json:"notification_channels"`
	MonitorNotifications []MonitorNotification `json:"monitor_notifications"`
	Incidents            []Incident            `json:"incidents"`
	Maintenances         []Maintenance         `json:"maintenances"`
	Subscribers          []Subscriber          `json:"subscribers"`
}

type PublicMonitorPageBinding struct {
	Page        StatusPage
	DisplayName string
}

type S3BackupObject struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

type S3BackupResult struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	UploadedAt   time.Time `json:"uploaded_at"`
	Pruned       int       `json:"pruned"`
	Monitors     int       `json:"monitors"`
	Heartbeats   int       `json:"heartbeats"`
	DailyRollups int       `json:"daily_rollups"`
}
