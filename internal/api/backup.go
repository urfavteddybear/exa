package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"exa/internal/backup"
	"exa/internal/model"
)

const (
	maxBackupUploadBytes  = 256 << 20
	maxBackupDecodedBytes = 512 << 20
)

func (rt *router) handleExportBackup(w http.ResponseWriter, r *http.Request) {
	data, err := rt.cfg.Store.ExportBackup()
	if err != nil {
		writeInternalError(w, r, "export backup failed", err)
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "gzip" || format == "gz" {
		gzBytes, err := backup.EncodeBackupGzip(data)
		if err != nil {
			writeInternalError(w, r, "encode gzip backup failed", err)
			return
		}
		filename := fmt.Sprintf("exa-backup-%s.json.gz", time.Now().UTC().Format("20060102-150405Z"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(gzBytes)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="exa-backup.json"`)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}

func (rt *router) handleImportBackup(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "empty backup payload")
		return
	}
	data, err := backup.DecodeBackupReader(r.Body, maxBackupUploadBytes, maxBackupDecodedBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := rt.cfg.Store.ImportBackup(*data); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rt.cfg.Monitors != nil {
		rt.cfg.Monitors.ReloadAll()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"monitors":      len(data.Monitors),
		"heartbeats":    len(data.Heartbeats),
		"daily_rollups": len(data.DailyRollups),
	})
}

func (rt *router) handleS3Test(w http.ResponseWriter, r *http.Request) {
	settings, err := rt.cfg.Store.GetAllSettings()
	if err != nil {
		writeInternalError(w, r, "load s3 settings failed", err)
		return
	}
	cfg := backup.LoadS3ConfigFromSettings(settings)

	if r.Body != nil {
		rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		if len(strings.TrimSpace(string(rawBody))) > 0 {
			var overrides map[string]any
			if err := json.Unmarshal(rawBody, &overrides); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON payload")
				return
			}
			applyS3TestOverrides(&cfg, overrides)
		}
	}

	client, err := backup.NewS3Client(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := client.TestConnection(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *router) handleS3Run(w http.ResponseWriter, r *http.Request) {
	settings, err := rt.cfg.Store.GetAllSettings()
	if err != nil {
		writeInternalError(w, r, "load s3 settings failed", err)
		return
	}
	cfg := backup.LoadS3ConfigFromSettings(settings)
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	runCtx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := backup.RunS3Backup(runCtx, rt.cfg.Store, cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (rt *router) handleS3List(w http.ResponseWriter, r *http.Request) {
	settings, err := rt.cfg.Store.GetAllSettings()
	if err != nil {
		writeInternalError(w, r, "load s3 settings failed", err)
		return
	}
	cfg := backup.LoadS3ConfigFromSettings(settings)
	client, err := backup.NewS3Client(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	list, err := client.ListObjects(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if list == nil {
		list = []model.S3BackupObject{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (rt *router) handleS3Restore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "backup key is required")
		return
	}

	settings, err := rt.cfg.Store.GetAllSettings()
	if err != nil {
		writeInternalError(w, r, "load s3 settings failed", err)
		return
	}
	cfg := backup.LoadS3ConfigFromSettings(settings)
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	data, err := backup.RestoreFromS3(r.Context(), rt.cfg.Store, cfg, req.Key)
	if err != nil {
		writeError(w, classifyS3ErrorStatus(err), err.Error())
		return
	}
	if rt.cfg.Monitors != nil {
		rt.cfg.Monitors.ReloadAll()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"monitors":      len(data.Monitors),
		"heartbeats":    len(data.Heartbeats),
		"daily_rollups": len(data.DailyRollups),
	})
}

func (rt *router) handleS3Delete(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" && r.Body != nil {
		var req struct {
			Key string `json:"key"`
		}
		if err := decodeJSON(r, &req); err == nil {
			key = strings.TrimSpace(req.Key)
		}
	}
	if key == "" {
		writeError(w, http.StatusBadRequest, "backup key is required")
		return
	}

	settings, err := rt.cfg.Store.GetAllSettings()
	if err != nil {
		writeInternalError(w, r, "load s3 settings failed", err)
		return
	}
	cfg := backup.LoadS3ConfigFromSettings(settings)
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := backup.DeleteS3Backup(r.Context(), cfg, key); err != nil {
		writeError(w, classifyS3ErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func applyS3TestOverrides(cfg *backup.S3Config, overrides map[string]any) {
	if v, ok := overrides["s3_endpoint"].(string); ok {
		cfg.Endpoint = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if v, ok := overrides["s3_region"].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			cfg.Region = trimmed
		}
	}
	if v, ok := overrides["s3_bucket"].(string); ok {
		cfg.Bucket = strings.TrimSpace(v)
	}
	if v, ok := overrides["s3_prefix"].(string); ok {
		cfg.Prefix = strings.TrimSpace(v)
	}
	if v, ok := overrides["s3_access_key_id"].(string); ok {
		cfg.AccessKeyID = strings.TrimSpace(v)
	}
	if v, ok := overrides["s3_secret_access_key"].(string); ok {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" && trimmed != "********" {
			cfg.SecretKey = trimmed
		}
	}
	if raw, exists := overrides["s3_force_path_style"]; exists {
		switch v := raw.(type) {
		case bool:
			cfg.ForcePathStyle = v
		case string:
			cfg.ForcePathStyle = strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
		}
	}
}

func classifyS3ErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var typeErr *json.UnmarshalTypeError
	msg := err.Error()
	if errors.As(err, &typeErr) ||
		strings.Contains(msg, "invalid") ||
		strings.Contains(msg, "prefix") ||
		strings.Contains(msg, "required") ||
		strings.Contains(msg, "unsupported backup version") ||
		strings.Contains(msg, "exceeds") {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}
