package backup

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

type mockS3Object struct {
	data         []byte
	lastModified time.Time
}

type mockS3Server struct {
	mu      sync.Mutex
	bucket  string
	objects map[string]mockS3Object
	srv     *httptest.Server
	lastAuth string
}

func newMockS3Server(bucket string) *mockS3Server {
	m := &mockS3Server{
		bucket:  bucket,
		objects: make(map[string]mockS3Object),
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	return m
}

func (m *mockS3Server) close() {
	m.srv.Close()
}

func (m *mockS3Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=testkey/") ||
		r.Header.Get("x-amz-date") == "" ||
		r.Header.Get("x-amz-content-sha256") == "" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>Missing SigV4 headers</Message></Error>`))
		return
	}

	prefix := "/" + m.bucket
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")

	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAuth = auth

	switch r.Method {
	case http.MethodGet:
		if key == "" && r.URL.Query().Get("list-type") == "2" {
			filterPrefix := r.URL.Query().Get("prefix")
			type itemXML struct {
				Key          string `xml:"Key"`
				LastModified string `xml:"LastModified"`
				Size         int64  `xml:"Size"`
			}
			var keys []string
			for k := range m.objects {
				if filterPrefix == "" || strings.HasPrefix(k, filterPrefix) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			var items []itemXML
			for _, k := range keys {
				obj := m.objects[k]
				items = append(items, itemXML{
					Key:          k,
					LastModified: obj.lastModified.UTC().Format(time.RFC3339),
					Size:         int64(len(obj.data)),
				})
			}
			res := struct {
				XMLName     xml.Name  `xml:"ListBucketResult"`
				IsTruncated bool      `xml:"IsTruncated"`
				Contents    []itemXML `xml:"Contents"`
			}{
				IsTruncated: false,
				Contents:    items,
			}
			w.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(w).Encode(res)
			return
		}
		obj, ok := m.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>Key not found</Message></Error>`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.data)

	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.objects[key] = mockS3Object{
			data:         body,
			lastModified: time.Now().UTC(),
		}
		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		delete(m.objects, key)
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestS3BackupRoundTripRetentionAndValidation(t *testing.T) {
	mock := newMockS3Server("exa-bucket")
	defer mock.close()

	dir := t.TempDir()
	store, err := db.OpenPath(filepath.Join(dir, "exa.db"))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, _, err = store.SetupInitialAdmin("admin", "admin@example.com", "hash123", "Exa Prod")
	if err != nil {
		t.Fatalf("SetupInitialAdmin: %v", err)
	}

	mon, err := store.CreateMonitor(model.Monitor{
		Name:   "API",
		Type:   model.MonitorTypeHTTP,
		Target: "https://api.example.com",
		Active: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	now := time.Now().UTC()
	ctx := context.Background()
	if err := store.RecordHeartbeat(ctx, mon.ID, model.ProbeResult{
		Status:     model.HeartbeatStatusUp,
		LatencyMs:  44,
		StatusCode: 200,
		Message:    "HTTP 200",
		CheckedAt:  now.Add(-2 * time.Minute),
	}, model.ComponentStatusOperational, 60); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}

	cfg := S3Config{
		Enabled:        true,
		Endpoint:       mock.srv.URL,
		Region:         "us-east-1",
		Bucket:         "exa-bucket",
		Prefix:         "prod/backups/",
		AccessKeyID:    "testkey",
		SecretKey:      "testsecret",
		ForcePathStyle: true,
		Schedule:       "daily",
		RetentionCount: 2,
	}

	client, err := NewS3Client(cfg)
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	if err := client.TestConnection(ctx); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}

	// Seed 2 older objects so running backup with RetentionCount=2 prunes the oldest.
	mock.mu.Lock()
	mock.objects["prod/backups/exa-backup-20250101-000000Z.json.gz"] = mockS3Object{
		data:         []byte("old1"),
		lastModified: now.Add(-48 * time.Hour),
	}
	mock.objects["prod/backups/exa-backup-20250102-000000Z.json.gz"] = mockS3Object{
		data:         []byte("old2"),
		lastModified: now.Add(-24 * time.Hour),
	}
	mock.mu.Unlock()

	res, err := RunS3Backup(ctx, store, cfg)
	if err != nil {
		t.Fatalf("RunS3Backup: %v", err)
	}
	if res.Monitors != 1 || res.Heartbeats != 1 || res.DailyRollups != 1 {
		t.Fatalf("unexpected backup counts: %+v", res)
	}
	if res.Pruned != 1 {
		t.Fatalf("expected 1 pruned backup, got %d", res.Pruned)
	}

	list, err := client.ListObjects(ctx)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(list) != 2 || list[0].Key != res.Key {
		t.Fatalf("expected newest backup first and total 2 retained, got %+v", list)
	}

	// Wipe monitors and heartbeats, then restore from S3.
	if err := store.DeleteMonitor(mon.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if _, err := RestoreFromS3(ctx, store, cfg, "../other-bucket/bad.json.gz"); err == nil {
		t.Fatal("expected traversal key to fail validation")
	}

	restored, err := RestoreFromS3(ctx, store, cfg, res.Key)
	if err != nil {
		t.Fatalf("RestoreFromS3: %v", err)
	}
	if len(restored.Heartbeats) != 1 || len(restored.DailyRollups) != 1 {
		t.Fatalf("expected restored history counts, got hb=%d ru=%d", len(restored.Heartbeats), len(restored.DailyRollups))
	}
	hbs, err := store.ListHeartbeats(mon.ID, 10)
	if err != nil || len(hbs) != 1 || hbs[0].LatencyMs != 44 {
		t.Fatalf("expected restored heartbeat latency 44, got %+v err=%v", hbs, err)
	}
	users, err := store.ListUsers()
	if err != nil || len(users) != 1 || users[0].Username != "admin" {
		t.Fatalf("expected admin user preserved, got %+v err=%v", users, err)
	}

	if err := DeleteS3Backup(ctx, cfg, res.Key); err != nil {
		t.Fatalf("DeleteS3Backup: %v", err)
	}
	afterDel, err := client.ListObjects(ctx)
	if err != nil || len(afterDel) != 1 {
		t.Fatalf("expected 1 remaining object after delete, got %+v err=%v", afterDel, err)
	}
}

func TestDecodeBackupReaderPlainAndSizeLimit(t *testing.T) {
	plain := []byte(`{"version":2,"monitors":[]}`)
	got, err := DecodeBackupReader(bytes.NewReader(plain), 1024, 1024)
	if err != nil || got.Version != 2 {
		t.Fatalf("expected plain JSON decode v2, got %+v err=%v", got, err)
	}
	if _, err := DecodeBackupReader(bytes.NewReader(plain), 8, 1024); err == nil {
		t.Fatal("expected raw size limit error")
	}
}
