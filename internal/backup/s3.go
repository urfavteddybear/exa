package backup

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

const (
	defaultS3Timeout     = 60 * time.Second
	defaultRetentionKeep = 14
	maxRawBackupBytes    = 256 << 20
	maxDecodedJSONBytes  = 512 << 20
)

type S3Config struct {
	Enabled        bool
	Endpoint       string
	Region         string
	Bucket         string
	Prefix         string
	AccessKeyID    string
	SecretKey      string
	ForcePathStyle bool
	Schedule       string
	RetentionCount int
}

func LoadS3ConfigFromSettings(settings map[string]string) S3Config {
	retention := defaultRetentionKeep
	if raw := strings.TrimSpace(settings["s3_retention_count"]); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			retention = n
		}
	}

	region := strings.TrimSpace(settings["s3_region"])
	if region == "" {
		region = "us-east-1"
	}

	prefix := strings.TrimSpace(settings["s3_prefix"])
	if _, ok := settings["s3_prefix"]; !ok {
		prefix = "exa-backups/"
	}

	forcePathStyle := true
	if raw := strings.TrimSpace(settings["s3_force_path_style"]); raw != "" {
		forcePathStyle = strings.EqualFold(raw, "true") || raw == "1"
	}

	schedule := strings.ToLower(strings.TrimSpace(settings["s3_schedule"]))
	if schedule == "" {
		schedule = "off"
	}

	return S3Config{
		Enabled:        strings.EqualFold(strings.TrimSpace(settings["s3_enabled"]), "true"),
		Endpoint:       strings.TrimRight(strings.TrimSpace(settings["s3_endpoint"]), "/"),
		Region:         region,
		Bucket:         strings.TrimSpace(settings["s3_bucket"]),
		Prefix:         prefix,
		AccessKeyID:    strings.TrimSpace(settings["s3_access_key_id"]),
		SecretKey:      strings.TrimSpace(settings["s3_secret_access_key"]),
		ForcePathStyle: forcePathStyle,
		Schedule:       schedule,
		RetentionCount: retention,
	}
}

func (c S3Config) NormalizePrefix() string {
	p := strings.TrimSpace(c.Prefix)
	p = strings.TrimLeft(p, "/")
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func (c S3Config) Validate() error {
	if strings.TrimSpace(c.Endpoint) == "" {
		return errors.New("s3_endpoint is required")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("s3_endpoint must be a valid http or https URL")
	}
	if strings.TrimSpace(c.Bucket) == "" {
		return errors.New("s3_bucket is required")
	}
	if strings.TrimSpace(c.AccessKeyID) == "" {
		return errors.New("s3_access_key_id is required")
	}
	if strings.TrimSpace(c.SecretKey) == "" {
		return errors.New("s3_secret_access_key is required")
	}
	if strings.TrimSpace(c.Region) == "" {
		return errors.New("s3_region is required")
	}
	if c.RetentionCount < 0 {
		return errors.New("s3_retention_count cannot be negative")
	}
	return nil
}

func ScheduleInterval(schedule string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(schedule)) {
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

type S3Client struct {
	cfg        S3Config
	httpClient *http.Client
	nowFn      func() time.Time
}

func NewS3Client(cfg S3Config) (*S3Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	return &S3Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: defaultS3Timeout,
		},
		nowFn: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (c *S3Client) TestConnection(ctx context.Context) error {
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	if p := c.cfg.NormalizePrefix(); p != "" {
		q.Set("prefix", p)
	}
	respBody, status, err := c.doSigned(ctx, http.MethodGet, "", q, nil, "")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return formatS3HTTPError(status, respBody)
	}
	return nil
}

func (c *S3Client) PutObject(ctx context.Context, key, contentType string, body []byte) error {
	if err := validateObjectKey(key, ""); err != nil {
		return err
	}
	respBody, status, err := c.doSigned(ctx, http.MethodPut, key, nil, body, contentType)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return formatS3HTTPError(status, respBody)
	}
	return nil
}

func (c *S3Client) GetObject(ctx context.Context, key string) ([]byte, error) {
	if err := validateObjectKey(key, ""); err != nil {
		return nil, err
	}
	respBody, status, err := c.doSigned(ctx, http.MethodGet, key, nil, nil, "")
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, formatS3HTTPError(status, respBody)
	}
	return respBody, nil
}

func (c *S3Client) DeleteObject(ctx context.Context, key string) error {
	if err := validateObjectKey(key, ""); err != nil {
		return err
	}
	respBody, status, err := c.doSigned(ctx, http.MethodDelete, key, nil, nil, "")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return formatS3HTTPError(status, respBody)
	}
	return nil
}

type listBucketResultXML struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		Size         int64  `xml:"Size"`
	} `xml:"Contents"`
}

func (c *S3Client) ListObjects(ctx context.Context) ([]model.S3BackupObject, error) {
	prefix := c.cfg.NormalizePrefix()
	out := []model.S3BackupObject{}
	token := ""

	for page := 0; page < 100; page++ {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("max-keys", "1000")
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}

		raw, status, err := c.doSigned(ctx, http.MethodGet, "", q, nil, "")
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, formatS3HTTPError(status, raw)
		}

		var res listBucketResultXML
		if err := xml.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("parse s3 list response: %w", err)
		}
		for _, item := range res.Contents {
			if !strings.HasSuffix(item.Key, ".json.gz") && !strings.HasSuffix(item.Key, ".json") {
				continue
			}
			lm, _ := parseS3Timestamp(item.LastModified)
			out = append(out, model.S3BackupObject{
				Key:          item.Key,
				Size:         item.Size,
				LastModified: lm,
			})
		}
		if !res.IsTruncated || res.NextContinuationToken == "" {
			break
		}
		token = res.NextContinuationToken
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastModified.Equal(out[j].LastModified) {
			return out[i].LastModified.After(out[j].LastModified)
		}
		return out[i].Key > out[j].Key
	})
	return out, nil
}

func (c *S3Client) buildTarget(key string, query url.Values) (targetURL, hostHeader, canonicalURI, canonicalQuery string, err error) {
	base, err := url.Parse(c.cfg.Endpoint)
	if err != nil {
		return "", "", "", "", err
	}

	host := base.Host
	basePath := strings.TrimRight(base.Path, "/")
	cleanKey := strings.TrimLeft(key, "/")

	var rawPath string
	if c.cfg.ForcePathStyle {
		rawPath = basePath + "/" + c.cfg.Bucket
		if cleanKey != "" {
			rawPath += "/" + cleanKey
		}
	} else {
		host = c.cfg.Bucket + "." + host
		rawPath = basePath + "/"
		if cleanKey != "" {
			rawPath = basePath + "/" + cleanKey
		}
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}

	canonicalURI = awsEncodePath(rawPath)
	canonicalQuery = canonicalizeQuery(query)
	hostHeader = stripDefaultPort(base.Scheme, host)

	targetURL = base.Scheme + "://" + host + canonicalURI
	if canonicalQuery != "" {
		targetURL += "?" + canonicalQuery
	}
	return targetURL, hostHeader, canonicalURI, canonicalQuery, nil
}

func (c *S3Client) doSigned(ctx context.Context, method, key string, query url.Values, body []byte, contentType string) ([]byte, int, error) {
	targetURL, hostHeader, canonicalURI, canonicalQuery, err := c.buildTarget(key, query)
	if err != nil {
		return nil, 0, err
	}

	now := c.nowFn().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(body)

	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", hostHeader, payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, c.cfg.Region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(c.cfg.SecretKey, dateStamp, c.cfg.Region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.cfg.AccessKeyID, scope, signedHeaders, signature,
	)

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	req.Host = hostHeader
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Authorization", authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxRawBackupBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(respBytes)) > maxRawBackupBytes {
		return nil, resp.StatusCode, fmt.Errorf("s3 object exceeds %d MiB limit", maxRawBackupBytes>>20)
	}
	return respBytes, resp.StatusCode, nil
}

func EncodeBackupGzip(data *model.BackupData) ([]byte, error) {
	if data == nil {
		return nil, errors.New("backup data is nil")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(data); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeBackupReader(r io.Reader, maxRawBytes, maxDecodedBytes int64) (*model.BackupData, error) {
	if maxRawBytes <= 0 {
		maxRawBytes = maxRawBackupBytes
	}
	if maxDecodedBytes <= 0 {
		maxDecodedBytes = maxDecodedJSONBytes
	}

	limited := io.LimitReader(r, maxRawBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read backup payload: %w", err)
	}
	if int64(len(raw)) > maxRawBytes {
		return nil, fmt.Errorf("backup payload exceeds %d MiB limit", maxRawBytes>>20)
	}
	if len(raw) == 0 {
		return nil, errors.New("empty backup payload")
	}

	br := bufio.NewReader(bytes.NewReader(raw))
	head, _ := br.Peek(2)
	var jsonReader io.Reader = br
	if len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("open gzip backup: %w", err)
		}
		defer gz.Close()
		jsonReader = io.LimitReader(gz, maxDecodedBytes+1)
	} else {
		jsonReader = io.LimitReader(br, maxDecodedBytes+1)
	}

	decodedBytes, err := io.ReadAll(jsonReader)
	if err != nil {
		return nil, fmt.Errorf("decompress backup: %w", err)
	}
	if int64(len(decodedBytes)) > maxDecodedBytes {
		return nil, fmt.Errorf("decompressed backup exceeds %d MiB limit", maxDecodedBytes>>20)
	}

	var data model.BackupData
	if err := json.Unmarshal(decodedBytes, &data); err != nil {
		return nil, errors.New("invalid backup JSON")
	}
	return &data, nil
}

func RunS3Backup(ctx context.Context, store *db.Store, cfg S3Config) (*model.S3BackupResult, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	client, err := NewS3Client(cfg)
	if err != nil {
		_ = store.SetSetting("s3_last_error", err.Error())
		return nil, err
	}

	data, err := store.ExportBackup()
	if err != nil {
		_ = store.SetSetting("s3_last_error", err.Error())
		return nil, err
	}
	gzBytes, err := EncodeBackupGzip(data)
	if err != nil {
		_ = store.SetSetting("s3_last_error", err.Error())
		return nil, err
	}

	now := time.Now().UTC()
	key := cfg.NormalizePrefix() + fmt.Sprintf("exa-backup-%s.json.gz", now.Format("20060102-150405Z"))
	if err := client.PutObject(ctx, key, "application/gzip", gzBytes); err != nil {
		_ = store.SetSetting("s3_last_error", err.Error())
		return nil, err
	}

	pruned := 0
	if cfg.RetentionCount > 0 {
		if objects, listErr := client.ListObjects(ctx); listErr == nil && len(objects) > cfg.RetentionCount {
			for _, obj := range objects[cfg.RetentionCount:] {
				if obj.Key == key {
					continue
				}
				if delErr := client.DeleteObject(ctx, obj.Key); delErr == nil {
					pruned++
				}
			}
		}
	}

	_ = store.SetSetting("s3_last_backup_at", now.Format(time.RFC3339))
	_ = store.SetSetting("s3_last_backup_key", key)
	_ = store.SetSetting("s3_last_error", "")

	return &model.S3BackupResult{
		Key:          key,
		Size:         int64(len(gzBytes)),
		UploadedAt:   now,
		Pruned:       pruned,
		Monitors:     len(data.Monitors),
		Heartbeats:   len(data.Heartbeats),
		DailyRollups: len(data.DailyRollups),
	}, nil
}

func RestoreFromS3(ctx context.Context, store *db.Store, cfg S3Config, key string) (*model.BackupData, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if err := validateObjectKey(key, cfg.NormalizePrefix()); err != nil {
		return nil, err
	}
	client, err := NewS3Client(cfg)
	if err != nil {
		return nil, err
	}
	raw, err := client.GetObject(ctx, key)
	if err != nil {
		return nil, err
	}
	data, err := DecodeBackupReader(bytes.NewReader(raw), maxRawBackupBytes, maxDecodedJSONBytes)
	if err != nil {
		return nil, err
	}
	if err := store.ImportBackup(*data); err != nil {
		return nil, err
	}
	return data, nil
}

func DeleteS3Backup(ctx context.Context, cfg S3Config, key string) error {
	if err := validateObjectKey(key, cfg.NormalizePrefix()); err != nil {
		return err
	}
	client, err := NewS3Client(cfg)
	if err != nil {
		return err
	}
	return client.DeleteObject(ctx, key)
}

func validateObjectKey(key, requiredPrefix string) error {
	k := strings.TrimSpace(key)
	if k == "" || strings.HasPrefix(k, "/") {
		return errors.New("invalid backup object key")
	}
	for _, seg := range strings.Split(k, "/") {
		if seg == ".." || seg == "." {
			return errors.New("invalid path segment in backup key")
		}
	}
	if requiredPrefix != "" && !strings.HasPrefix(k, requiredPrefix) {
		return errors.New("backup key does not match configured prefix")
	}
	return nil
}

func formatS3HTTPError(status int, body []byte) error {
	var xmlErr struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(body, &xmlErr) == nil && (xmlErr.Code != "" || xmlErr.Message != "") {
		return fmt.Errorf("s3 error HTTP %d (%s): %s", status, xmlErr.Code, xmlErr.Message)
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 256 {
		snippet = snippet[:256] + "..."
	}
	if snippet != "" {
		return fmt.Errorf("s3 error HTTP %d: %s", status, snippet)
	}
	return fmt.Errorf("s3 error HTTP %d", status)
}

func parseS3Timestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", raw)
}

func stripDefaultPort(scheme, host string) string {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	if (scheme == "http" && p == "80") || (scheme == "https" && p == "443") {
		if strings.Contains(h, ":") {
			return "[" + h + "]"
		}
		return h
	}
	return host
}

func awsEncodePath(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = awsURIEscape(seg)
	}
	res := strings.Join(parts, "/")
	if !strings.HasPrefix(res, "/") {
		return "/" + res
	}
	return res
}

func canonicalizeQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		encK := awsURIEscape(k)
		for _, v := range vals {
			pairs = append(pairs, encK+"="+awsURIEscape(v))
		}
	}
	return strings.Join(pairs, "&")
}

func awsURIEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlpha := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		isDigit := c >= '0' && c <= '9'
		if isAlpha || isDigit || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}
