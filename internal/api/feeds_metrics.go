package api

import (
	"crypto/subtle"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

func (rt *router) authorizeBadgeMonitor(w http.ResponseWriter, r *http.Request) (*model.Monitor, bool) {
	slug := r.PathValue("slug")
	mon, err := rt.cfg.Store.GetMonitorBySlug(slug)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return nil, false
		}
		writeInternalError(w, r, "lookup badge monitor failed", err)
		return nil, false
	}

	onPublic, err := rt.cfg.Store.IsMonitorOnPublicStatusPage(mon.ID)
	if err != nil {
		writeInternalError(w, r, "check monitor public visibility failed", err)
		return nil, false
	}
	if !onPublic {
		if _, ok := rt.cfg.Auth.AuthenticateRequest(r); !ok {
			writeError(w, http.StatusNotFound, "monitor not found")
			return nil, false
		}
	}
	return mon, true
}

func (rt *router) handleBadgeStatus(w http.ResponseWriter, r *http.Request) {
	mon, ok := rt.authorizeBadgeMonitor(w, r)
	if !ok {
		return
	}

	valLabel, color := statusBadgeAppearance(mon.CurrentStatus)
	svg := renderShieldSVG(mon.Name, valLabel, color)
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, max-age=30")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(svg))
}

func (rt *router) handleBadgeUptime(w http.ResponseWriter, r *http.Request) {
	mon, ok := rt.authorizeBadgeMonitor(w, r)
	if !ok {
		return
	}

	now := time.Now().UTC()
	fromDay := now.AddDate(0, 0, -89).Format("2006-01-02")
	toDay := now.Format("2006-01-02")
	rollups, err := rt.cfg.Store.GetDailyRollups(mon.ID, fromDay, toDay)
	if err != nil {
		writeInternalError(w, r, "get badge rollups failed", err)
		return
	}

	var total, up int
	for _, ru := range rollups {
		total += ru.TotalChecks
		up += ru.UpChecks + ru.DegradedChecks
	}

	val := "No data"
	color := "#4B5563"
	if total > 0 {
		pct := float64(up) / float64(total) * 100.0
		val = fmt.Sprintf("%.2f%%", pct)
		switch {
		case pct >= 99.0:
			color = "#15803D"
		case pct >= 95.0:
			color = "#B45309"
		default:
			color = "#B91C1C"
		}
	}

	svg := renderShieldSVG("uptime 90d", val, color)
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(svg))
}

func statusBadgeAppearance(st model.ComponentStatus) (string, string) {
	switch st {
	case model.ComponentStatusOperational:
		return "Operational", "#15803D"
	case model.ComponentStatusDegraded:
		return "Degraded", "#B45309"
	case model.ComponentStatusMajorOutage:
		return "Major Outage", "#B91C1C"
	case model.ComponentStatusMaintenance:
		return "Maintenance", "#1D4ED8"
	default:
		return "Unknown", "#4B5563"
	}
}

func renderShieldSVG(label, value, valueColor string) string {
	leftW := max(len(label)*7+16, 54)
	rightW := max(len(value)*7+16, 54)
	totalW := leftW + rightW
	leftX := leftW / 2
	rightX := leftW + rightW/2

	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`+
			`<rect width="%d" height="20" rx="3" fill="#1F2937"/>`+
			`<rect x="%d" width="%d" height="20" rx="3" fill="%s"/>`+
			`<rect x="%d" width="4" height="20" fill="%s"/>`+
			`<g fill="#FFFFFF" text-anchor="middle" font-family="IBM Plex Sans, -apple-system, BlinkMacSystemFont, sans-serif" font-size="11" font-weight="600">`+
			`<text x="%d" y="14">%s</text>`+
			`<text x="%d" y="14">%s</text>`+
			`</g></svg>`,
		totalW, html.EscapeString(label), html.EscapeString(value),
		totalW,
		leftW, rightW, valueColor,
		leftW, valueColor,
		leftX, html.EscapeString(label),
		rightX, html.EscapeString(value),
	)
}

func (rt *router) handleRSSDefault(w http.ResponseWriter, r *http.Request) {
	rt.serveRSS(w, r, "")
}

func (rt *router) handleRSSBySlug(w http.ResponseWriter, r *http.Request) {
	rt.serveRSS(w, r, r.PathValue("slug"))
}

func (rt *router) handleAtomDefault(w http.ResponseWriter, r *http.Request) {
	rt.serveAtom(w, r, "")
}

func (rt *router) handleAtomBySlug(w http.ResponseWriter, r *http.Request) {
	rt.serveAtom(w, r, r.PathValue("slug"))
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	PubDate     string    `xml:"pubDate"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func (rt *router) serveRSS(w http.ResponseWriter, r *http.Request, slug string) {
	page, err := rt.resolvePublicPage(r, slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "status page not found")
		return
	}
	incidents, err := rt.cfg.Store.ListIncidents(page.ID, 30, 0)
	if err != nil {
		writeInternalError(w, r, "feed generation failed", err)
		return
	}

	baseURL := rt.resolveBaseURL(r)
	pageURL := baseURL + "/s/" + page.Slug
	if page.IsDefault {
		pageURL = baseURL + "/"
	}

	items := make([]rssItem, 0, len(incidents))
	for _, inc := range incidents {
		desc := string(inc.Status)
		if len(inc.Updates) > 0 {
			desc = fmt.Sprintf("[%s] %s", inc.Updates[0].Status, inc.Updates[0].Message)
		}
		items = append(items, rssItem{
			Title:       fmt.Sprintf("[%s] %s", inc.Impact, inc.Title),
			Link:        pageURL + "#incident-" + inc.ID,
			GUID:        inc.ID,
			PubDate:     inc.CreatedAt.UTC().Format(time.RFC1123Z),
			Description: desc,
		})
	}

	doc := rssDocument{
		Version: "2.0",
		Channel: rssChannel{
			Title:       page.Title + " Incidents",
			Link:        pageURL,
			Description: page.Description,
			PubDate:     time.Now().UTC().Format(time.RFC1123Z),
			Items:       items,
		},
	}

	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		writeInternalError(w, r, "feed generation failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Xmlns   string      `xml:"xmlns,attr"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Link    atomLink    `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}

type atomEntry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Summary string   `xml:"summary"`
}

func (rt *router) serveAtom(w http.ResponseWriter, r *http.Request, slug string) {
	page, err := rt.resolvePublicPage(r, slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "status page not found")
		return
	}
	incidents, err := rt.cfg.Store.ListIncidents(page.ID, 30, 0)
	if err != nil {
		writeInternalError(w, r, "feed generation failed", err)
		return
	}

	baseURL := rt.resolveBaseURL(r)
	pageURL := baseURL + "/s/" + page.Slug
	if page.IsDefault {
		pageURL = baseURL + "/"
	}

	entries := make([]atomEntry, 0, len(incidents))
	for _, inc := range incidents {
		summary := string(inc.Status)
		if len(inc.Updates) > 0 {
			summary = fmt.Sprintf("[%s] %s", inc.Updates[0].Status, inc.Updates[0].Message)
		}
		entries = append(entries, atomEntry{
			Title:   fmt.Sprintf("[%s] %s", inc.Impact, inc.Title),
			ID:      "urn:exa:incident:" + inc.ID,
			Updated: inc.UpdatedAt.UTC().Format(time.RFC3339),
			Link:    atomLink{Href: pageURL + "#incident-" + inc.ID},
			Summary: summary,
		})
	}

	doc := atomFeed{
		Xmlns:   "http://www.w3.org/2005/Atom",
		Title:   page.Title + " Incidents",
		ID:      "urn:exa:page:" + page.ID,
		Updated: time.Now().UTC().Format(time.RFC3339),
		Link:    atomLink{Href: pageURL, Rel: "alternate"},
		Entries: entries,
	}

	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		writeInternalError(w, r, "feed generation failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

func (rt *router) resolveBaseURL(r *http.Request) string {
	if configured, err := rt.cfg.Store.GetSetting("base_url"); err == nil && strings.TrimSpace(configured) != "" {
		return strings.TrimRight(strings.TrimSpace(configured), "/")
	}
	scheme := "http"
	if r.TLS != nil || (rt.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	if r.Host != "" {
		return scheme + "://" + r.Host
	}
	return "http://localhost:8080"
}

func (rt *router) handleMetrics(w http.ResponseWriter, r *http.Request) {
	token, err := rt.cfg.Store.GetSetting("metrics_token")
	if err != nil {
		writeInternalError(w, r, "read metrics_token failed", err)
		return
	}
	token = strings.TrimSpace(token)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "metrics endpoint requires metrics_token to be configured")
		return
	}
	authHdr := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(authHdr) <= len(prefix) || !strings.EqualFold(authHdr[:len(prefix)], prefix) {
		writeError(w, http.StatusUnauthorized, "metrics bearer token required")
		return
	}
	provided := strings.TrimSpace(authHdr[len(prefix):])
	if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid metrics bearer token")
		return
	}

	monitors, err := rt.cfg.Store.ListMonitors()
	if err != nil {
		writeInternalError(w, r, "list metrics monitors failed", err)
		return
	}

	var b strings.Builder
	b.WriteString("# HELP exa_uptime_seconds Process uptime in seconds.\n")
	b.WriteString("# TYPE exa_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "exa_uptime_seconds %.0f\n", time.Since(rt.cfg.StartedAt).Seconds())

	b.WriteString("# HELP exa_monitor_up Monitor operational state (1=operational, 0.5=degraded, 0=major_outage, -1=unknown/maintenance).\n")
	b.WriteString("# TYPE exa_monitor_up gauge\n")
	for _, m := range monitors {
		val := -1.0
		switch m.CurrentStatus {
		case model.ComponentStatusOperational:
			val = 1.0
		case model.ComponentStatusDegraded:
			val = 0.5
		case model.ComponentStatusMajorOutage:
			val = 0.0
		}
		fmt.Fprintf(&b, "exa_monitor_up{id=%q,slug=%q,name=%q,type=%q} %g\n", m.ID, m.Slug, m.Name, m.Type, val)
	}

	b.WriteString("# HELP exa_monitor_latency_ms Last recorded probe latency in milliseconds.\n")
	b.WriteString("# TYPE exa_monitor_latency_ms gauge\n")
	for _, m := range monitors {
		if m.LastLatencyMs != nil {
			fmt.Fprintf(&b, "exa_monitor_latency_ms{id=%q,slug=%q,name=%q,type=%q} %d\n", m.ID, m.Slug, m.Name, m.Type, *m.LastLatencyMs)
		}
	}

	b.WriteString("# HELP exa_monitor_tls_days_left Days remaining until TLS certificate expiry.\n")
	b.WriteString("# TYPE exa_monitor_tls_days_left gauge\n")
	for _, m := range monitors {
		if m.TLSDaysLeft != nil {
			fmt.Fprintf(&b, "exa_monitor_tls_days_left{id=%q,slug=%q,name=%q,type=%q} %d\n", m.ID, m.Slug, m.Name, m.Type, *m.TLSDaysLeft)
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}
