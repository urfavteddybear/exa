package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

const defaultNotifyTimeout = 10 * time.Second

// Dispatcher routes monitor, incident, and maintenance notifications to channels and subscribers.
type Dispatcher struct {
	store            *db.Store
	client           *http.Client
	subscriberClient *http.Client
	telegramBaseURL  string
}

// NewDispatcher creates a Dispatcher backed by store.
func NewDispatcher(store *db.Store) *Dispatcher {
	return &Dispatcher{
		store: store,
		client: &http.Client{
			Timeout: defaultNotifyTimeout,
		},
		subscriberClient: newSubscriberHTTPClient(),
		telegramBaseURL:  "https://api.telegram.org",
	}
}

func newSubscriberHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultNotifyTimeout,
		Transport: &http.Transport{
			DialContext:       subscriberDialContext(nil),
			DisableKeepAlives: true,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are not allowed for subscriber webhooks")
		},
	}
}

type notificationPayload struct {
	Event          string                `json:"event"`
	Title          string                `json:"title"`
	Message        string                `json:"message"`
	Severity       string                `json:"severity"`
	Timestamp      time.Time             `json:"timestamp"`
	Monitor        *monitorEventPayload  `json:"monitor,omitempty"`
	StatusPage     *pageEventPayload     `json:"status_page,omitempty"`
	Incident       *model.Incident       `json:"incident,omitempty"`
	IncidentUpdate *model.IncidentUpdate `json:"incident_update,omitempty"`
	Maintenance    *model.Maintenance    `json:"maintenance,omitempty"`
}

type monitorEventPayload struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Slug           string                `json:"slug"`
	Type           model.MonitorType     `json:"type"`
	Target         string                `json:"target"`
	PreviousStatus model.ComponentStatus `json:"previous_status"`
	CurrentStatus  model.ComponentStatus `json:"current_status"`
	Error          string                `json:"error,omitempty"`
}

type pageEventPayload struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// NotifyMonitorStateChange notifies channels attached to m (or default channels).
func (d *Dispatcher) NotifyMonitorStateChange(ctx context.Context, m model.Monitor, prev, next model.ComponentStatus, errMsg string) {
	if d == nil || d.store == nil {
		return
	}
	channels, err := d.store.ListChannelsForMonitor(m.ID)
	if err != nil || len(channels) == 0 {
		return
	}

	msg := fmt.Sprintf("Monitor %s (%s) changed from %s to %s.", m.Name, m.Target, prev, next)
	if errMsg != "" {
		msg += " Details: " + errMsg
	}

	payload := notificationPayload{
		Event:     "monitor_state_change",
		Title:     fmt.Sprintf("[%s] %s is %s", statusTag(next), m.Name, next),
		Message:   msg,
		Severity:  severityForComponentStatus(next),
		Timestamp: time.Now().UTC(),
		Monitor: &monitorEventPayload{
			ID:             m.ID,
			Name:           m.Name,
			Slug:           m.Slug,
			Type:           m.Type,
			Target:         m.Target,
			PreviousStatus: prev,
			CurrentStatus:  next,
			Error:          errMsg,
		},
	}

	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		_ = d.sendChannel(ctx, ch, payload)
	}
}

// NotifyIncidentUpdate notifies enabled default channels and active page subscribers.
func (d *Dispatcher) NotifyIncidentUpdate(ctx context.Context, page model.StatusPage, inc model.Incident, upd model.IncidentUpdate) {
	if d == nil || d.store == nil {
		return
	}

	msg := fmt.Sprintf("Incident %q (%s impact) status is %s: %s", inc.Title, inc.Impact, upd.Status, upd.Message)
	payload := notificationPayload{
		Event:     "incident_update",
		Title:     fmt.Sprintf("[%s] %s: %s", upd.Status, page.Title, inc.Title),
		Message:   msg,
		Severity:  severityForIncident(inc.Impact, upd.Status),
		Timestamp: time.Now().UTC(),
		StatusPage: &pageEventPayload{
			ID:    page.ID,
			Slug:  page.Slug,
			Title: page.Title,
		},
		Incident:       &inc,
		IncidentUpdate: &upd,
	}

	d.dispatchPageNotifications(ctx, page.ID, payload)
}

// NotifyMaintenanceUpdate notifies enabled default channels and active page subscribers.
func (d *Dispatcher) NotifyMaintenanceUpdate(ctx context.Context, page model.StatusPage, maint model.Maintenance) {
	if d == nil || d.store == nil {
		return
	}

	msg := fmt.Sprintf(
		"Maintenance %q (%s to %s) is %s.",
		maint.Title,
		maint.ScheduledStart.UTC().Format(time.RFC3339),
		maint.ScheduledEnd.UTC().Format(time.RFC3339),
		maint.Status,
	)
	if maint.Description != "" {
		msg += " " + maint.Description
	}

	sev := "info"
	if maint.Status == model.MaintenanceStatusCompleted {
		sev = "resolved"
	}

	payload := notificationPayload{
		Event:     "maintenance_update",
		Title:     fmt.Sprintf("[maintenance:%s] %s: %s", maint.Status, page.Title, maint.Title),
		Message:   msg,
		Severity:  sev,
		Timestamp: time.Now().UTC(),
		StatusPage: &pageEventPayload{
			ID:    page.ID,
			Slug:  page.Slug,
			Title: page.Title,
		},
		Maintenance: &maint,
	}

	d.dispatchPageNotifications(ctx, page.ID, payload)
}

// TestChannel sends a test notification through the provided channel configuration.
func (d *Dispatcher) TestChannel(ctx context.Context, ch model.NotificationChannel) error {
	payload := notificationPayload{
		Event:     "test",
		Title:     "Exa Test Notification",
		Message:   fmt.Sprintf("Test notification for channel %q (%s).", ch.Name, ch.Type),
		Severity:  "info",
		Timestamp: time.Now().UTC(),
	}
	return d.sendChannel(ctx, ch, payload)
}

func (d *Dispatcher) dispatchPageNotifications(ctx context.Context, pageID string, payload notificationPayload) {
	channels, err := d.store.ListNotificationChannels()
	var emailCfg *model.EmailConfig
	if err == nil {
		for _, ch := range channels {
			if !ch.Enabled {
				continue
			}
			if ch.Type == model.NotificationTypeEmail && emailCfg == nil {
				var parsed model.EmailConfig
				if jsonErr := json.Unmarshal(ch.Config, &parsed); jsonErr == nil && parsed.SMTPHost != "" {
					emailCfg = &parsed
				}
			}
			if ch.IsDefault {
				_ = d.sendChannel(ctx, ch, payload)
			}
		}
	}

	subs, err := d.store.ListActiveSubscribers(pageID)
	if err != nil {
		return
	}
	for _, sub := range subs {
		switch sub.Type {
		case model.SubscriberTypeWebhook:
			_ = d.sendSubscriberWebhook(ctx, sub.Endpoint, payload)
		case model.SubscriberTypeEmail:
			if emailCfg != nil {
				subEmail := *emailCfg
				subEmail.To = []string{sub.Endpoint}
				_ = d.sendEmail(ctx, subEmail, payload)
			}
		}
	}
}

func (d *Dispatcher) sendChannel(ctx context.Context, ch model.NotificationChannel, payload notificationPayload) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultNotifyTimeout)
		defer cancel()
	}

	switch ch.Type {
	case model.NotificationTypeWebhook:
		var cfg model.WebhookConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid webhook config: %w", err)
		}
		return d.sendWebhook(ctx, cfg, payload)
	case model.NotificationTypeDiscord:
		var cfg model.DiscordConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid discord config: %w", err)
		}
		return d.sendDiscord(ctx, cfg, payload)
	case model.NotificationTypeSlack:
		var cfg model.SlackConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid slack config: %w", err)
		}
		return d.sendSlack(ctx, cfg, payload)
	case model.NotificationTypeTelegram:
		var cfg model.TelegramConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid telegram config: %w", err)
		}
		return d.sendTelegram(ctx, cfg, payload)
	case model.NotificationTypeEmail:
		var cfg model.EmailConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid email config: %w", err)
		}
		return d.sendEmail(ctx, cfg, payload)
	case model.NotificationTypeNtfy:
		var cfg model.NtfyConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid ntfy config: %w", err)
		}
		return d.sendNtfy(ctx, cfg, payload)
	default:
		return fmt.Errorf("unsupported notification channel type: %s", ch.Type)
	}
}

func statusTag(st model.ComponentStatus) string {
	switch st {
	case model.ComponentStatusOperational:
		return "OK"
	case model.ComponentStatusDegraded:
		return "DEGRADED"
	case model.ComponentStatusMajorOutage:
		return "DOWN"
	case model.ComponentStatusMaintenance:
		return "MAINT"
	default:
		return "INFO"
	}
}

func severityForComponentStatus(st model.ComponentStatus) string {
	switch st {
	case model.ComponentStatusOperational:
		return "resolved"
	case model.ComponentStatusDegraded:
		return "warning"
	case model.ComponentStatusMajorOutage:
		return "critical"
	default:
		return "info"
	}
}

func severityForIncident(impact model.IncidentImpact, status model.IncidentStatus) string {
	if status == model.IncidentStatusResolved {
		return "resolved"
	}
	switch impact {
	case model.IncidentImpactMajor, model.IncidentImpactCritical:
		return "critical"
	case model.IncidentImpactMinor:
		return "warning"
	default:
		return "info"
	}
}
