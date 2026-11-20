package db

import (
	"database/sql"
	"fmt"
	"time"

	"exa/internal/model"
)

func (s *Store) IsSetupCompleted() (bool, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) SetupInitialAdmin(username, email, passwordHash, siteTitle string) (*model.User, *model.StatusPage, error) {
	if siteTitle == "" {
		siteTitle = "Exa Status"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var userCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return nil, nil, err
	}
	if userCount > 0 {
		return nil, nil, ErrSetupCompleted
	}

	now := time.Now().UTC()
	nowStr := formatTime(now)

	user := model.User{
		ID:           newID(),
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		Role:         model.UserRoleAdmin,
		CreatedAt:    now,
	}
	if _, err := tx.Exec(
		`INSERT INTO users (id, username, email, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.Email, user.PasswordHash, string(user.Role), nowStr,
	); err != nil {
		return nil, nil, fmt.Errorf("insert admin user: %w", err)
	}

	var pageCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM status_pages`).Scan(&pageCount); err != nil {
		return nil, nil, err
	}

	var page model.StatusPage
	if pageCount == 0 {
		page = model.StatusPage{
			ID:          newID(),
			Slug:        "default",
			Title:       siteTitle,
			Description: "Service status and uptime history",
			IsDefault:   true,
			IsPublic:    true,
			ShowLatency: true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if _, err := tx.Exec(
			`INSERT INTO status_pages (id, slug, title, description, custom_css, is_default, is_public, show_latency, created_at, updated_at)
			 VALUES (?, ?, ?, ?, '', 1, 1, 1, ?, ?)`,
			page.ID, page.Slug, page.Title, page.Description, nowStr, nowStr,
		); err != nil {
			return nil, nil, fmt.Errorf("insert default status page: %w", err)
		}
	} else {
		row := tx.QueryRow(`SELECT id, slug, title, description, custom_css, is_default, is_public, show_latency, created_at, updated_at FROM status_pages WHERE is_default = 1 LIMIT 1`)
		var isDef, isPub, showLat int
		var createdStr, updatedStr string
		if err := row.Scan(&page.ID, &page.Slug, &page.Title, &page.Description, &page.CustomCSS, &isDef, &isPub, &showLat, &createdStr, &updatedStr); err == nil {
			page.IsDefault = isDef == 1
			page.IsPublic = isPub == 1
			page.ShowLatency = showLat == 1
			page.CreatedAt = parseTime(createdStr)
			page.UpdatedAt = parseTime(updatedStr)
		}
	}

	defaults := map[string]string{
		"setup_completed":     "true",
		"site_title":          siteTitle,
		"retention_days":      "90",
		"allow_subscriptions": "false",
	}
	for k, v := range defaults {
		if _, err := tx.Exec(
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			k, v,
		); err != nil {
			return nil, nil, fmt.Errorf("set default setting %s: %w", k, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return &user, &page, nil
}

func (s *Store) CreateUser(u model.User) (*model.User, error) {
	if u.ID == "" {
		u.ID = newID()
	}
	if u.Role == "" {
		u.Role = model.UserRoleViewer
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO users (id, username, email, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Email, u.PasswordHash, string(u.Role), formatTime(u.CreatedAt),
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByUsername(username string) (*model.User, error) {
	return scanUser(s.db.QueryRow(
		`SELECT id, username, email, password_hash, role, created_at FROM users WHERE username = ?`,
		username,
	))
}

func (s *Store) GetUserByID(id string) (*model.User, error) {
	return scanUser(s.db.QueryRow(
		`SELECT id, username, email, password_hash, role, created_at FROM users WHERE id = ?`,
		id,
	))
}

func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(`SELECT id, username, email, password_hash, role, created_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []model.User{}
	for rows.Next() {
		var u model.User
		var role, createdStr string
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &role, &createdStr); err != nil {
			return nil, err
		}
		u.Role = model.UserRole(role)
		u.CreatedAt = parseTime(createdStr)
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) DeleteUser(id string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateUserPassword(id, passwordHash string) error {
	res, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(id, userID string, expiresAt time.Time) (*model.Session, error) {
	if id == "" {
		id = newToken(32)
	}
	now := time.Now().UTC()
	sess := model.Session{
		ID:        id,
		UserID:    userID,
		ExpiresAt: expiresAt.UTC(),
		CreatedAt: now,
	}
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		sess.ID, sess.UserID, formatTime(sess.ExpiresAt), formatTime(sess.CreatedAt),
	)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) GetSessionUser(sessionID string) (*model.User, error) {
	nowStr := formatTime(time.Now().UTC())
	return scanUser(s.db.QueryRow(
		`SELECT u.id, u.username, u.email, u.password_hash, u.role, u.created_at
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.id = ? AND s.expires_at > ?`,
		sessionID, nowStr,
	))
}

func (s *Store) DeleteSession(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

func (s *Store) DeleteUserSessions(userID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) CleanExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, formatTime(time.Now().UTC()))
	return err
}

func (s *Store) CreateAPIKey(k model.APIKey) (*model.APIKey, error) {
	if k.ID == "" {
		k.ID = newID()
	}
	if k.Role == "" {
		k.Role = model.UserRoleAdmin
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO api_keys (id, name, key_prefix, key_hash, role, last_used_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.Name, k.KeyPrefix, k.KeyHash, string(k.Role), formatNullTime(k.LastUsedAt), formatTime(k.CreatedAt),
	)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *Store) GetAPIKeyByHash(hash string) (*model.APIKey, error) {
	var k model.APIKey
	var role, createdStr string
	var lastUsed sql.NullString
	err := s.db.QueryRow(
		`SELECT id, name, key_prefix, key_hash, role, last_used_at, created_at FROM api_keys WHERE key_hash = ?`,
		hash,
	).Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.KeyHash, &role, &lastUsed, &createdStr)
	if err != nil {
		return nil, mapErr(err)
	}
	k.Role = model.UserRole(role)
	k.LastUsedAt = parseNullTime(lastUsed)
	k.CreatedAt = parseTime(createdStr)
	return &k, nil
}

func (s *Store) TouchAPIKey(id string) error {
	_, err := s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), id)
	return err
}

func (s *Store) ListAPIKeys() ([]model.APIKey, error) {
	rows, err := s.db.Query(`SELECT id, name, key_prefix, key_hash, role, last_used_at, created_at FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := []model.APIKey{}
	for rows.Next() {
		var k model.APIKey
		var role, createdStr string
		var lastUsed sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.KeyHash, &role, &lastUsed, &createdStr); err != nil {
			return nil, err
		}
		k.Role = model.UserRole(role)
		k.LastUsedAt = parseNullTime(lastUsed)
		k.CreatedAt = parseTime(createdStr)
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Store) DeleteAPIKey(id string) error {
	res, err := s.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanUser(row *sql.Row) (*model.User, error) {
	var u model.User
	var role, createdStr string
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &role, &createdStr); err != nil {
		return nil, mapErr(err)
	}
	u.Role = model.UserRole(role)
	u.CreatedAt = parseTime(createdStr)
	return &u, nil
}
