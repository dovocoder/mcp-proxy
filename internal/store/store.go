package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

// Store is the SQLite data access layer.
type Store struct {
	db *sql.DB
}

// New creates a new Store, opening the database and running migrations.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite doesn't handle concurrent writes well

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS servers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		transport TEXT NOT NULL DEFAULT 'stdio',
		command TEXT NOT NULL DEFAULT '',
		args TEXT NOT NULL DEFAULT '[]',
		url TEXT NOT NULL DEFAULT '',
		headers TEXT NOT NULL DEFAULT '{}',
		env TEXT NOT NULL DEFAULT '{}',
		auth_token TEXT NOT NULL DEFAULT '',
		timeout INTEGER NOT NULL DEFAULT 120,
		connect_timeout INTEGER NOT NULL DEFAULT 60,
		enabled INTEGER NOT NULL DEFAULT 1,
		status TEXT NOT NULL DEFAULT 'disconnected',
		last_seen DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS api_keys (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		key_hash TEXT NOT NULL,
		key_prefix TEXT NOT NULL,
		scopes TEXT NOT NULL DEFAULT '[]',
		active INTEGER NOT NULL DEFAULT 1,
		last_used_at DATETIME,
		expires_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'admin',
		oidc_subject TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS oauth_tokens (
		server_id TEXT PRIMARY KEY,
		access_token TEXT NOT NULL,
		token_type TEXT NOT NULL DEFAULT 'Bearer',
		refresh_token TEXT NOT NULL DEFAULT '',
		expires_at DATETIME,
		scope TEXT NOT NULL DEFAULT '',
		client_id TEXT NOT NULL DEFAULT '',
		client_secret TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS compound_servers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		description TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS compound_members (
		compound_id TEXT NOT NULL,
		server_id TEXT NOT NULL,
		PRIMARY KEY (compound_id, server_id)
	);

	CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		palace TEXT NOT NULL DEFAULT 'general',
		room TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		tags TEXT NOT NULL DEFAULT '[]',
		importance INTEGER NOT NULL DEFAULT 50,
		access_count INTEGER NOT NULL DEFAULT 0,
		last_accessed DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_memories_palace ON memories(palace);
	CREATE INDEX IF NOT EXISTS idx_memories_content ON memories(content);

	CREATE TABLE IF NOT EXISTS memory_sets (
		id TEXT PRIMARY KEY,
		name TEXT,
		slug TEXT UNIQUE,
		description TEXT,
		is_default INTEGER DEFAULT 0,
		created_at TEXT
	);

	CREATE TABLE IF NOT EXISTS env_vars (
		id TEXT PRIMARY KEY,
		project TEXT NOT NULL,
		environment TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(project, environment, key)
	);

	CREATE TABLE IF NOT EXISTS disabled_tools (
		id TEXT PRIMARY KEY,
		tool_name TEXT NOT NULL,
		server_id TEXT,
		created_at TEXT NOT NULL,
		UNIQUE(tool_name, server_id)
	);

	CREATE TABLE IF NOT EXISTS oauth_registrations (
		issuer TEXT PRIMARY KEY,
		client_id TEXT NOT NULL,
		client_secret TEXT NOT NULL DEFAULT '',
		registration_access_token TEXT NOT NULL DEFAULT '',
		client_id_issued_at INTEGER NOT NULL DEFAULT 0,
		client_secret_expires_at INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Migration: add auth_token column if it doesn't exist (for existing DBs)
	_, err = db.Exec(`ALTER TABLE servers ADD COLUMN auth_token TEXT NOT NULL DEFAULT ''`)
	if err != nil {
		// Column already exists — this is expected
	}

	// Migration: add compound_id column to api_keys
	_, err = db.Exec(`ALTER TABLE api_keys ADD COLUMN compound_id TEXT`)
	if err != nil {
		// Column already exists
	}

	// Migration: add dictionary_mode column to compound_servers
	_, err = db.Exec(`ALTER TABLE compound_servers ADD COLUMN dictionary_mode INTEGER NOT NULL DEFAULT 0`)
	if err != nil {
		// Column already exists
	}

	// Migration: add set_id column to memories
	_, err = db.Exec(`ALTER TABLE memories ADD COLUMN set_id TEXT DEFAULT 'default'`)
	if err != nil {
		// Column already exists
	}

	// Migration: add oidc_subject column to users
	_, err = db.Exec(`ALTER TABLE users ADD COLUMN oidc_subject TEXT NOT NULL DEFAULT ''`)
	if err != nil {
		// Column already exists
	}

	// Migration: add logs_enabled column to servers
	_, err = db.Exec(`ALTER TABLE servers ADD COLUMN logs_enabled INTEGER NOT NULL DEFAULT 1`)
	if err != nil {
		// Column already exists
	}

	// Create default memory set if it doesn't exist
	_, _ = db.Exec(`INSERT OR IGNORE INTO memory_sets (id, name, slug, description, is_default, created_at) VALUES ('default', 'Default', '', '', 1, datetime('now'))`)

	return nil
}

// --- Servers ---

// CreateServer inserts a new server.
func (s *Store) CreateServer(srv *models.Server) error {
	argsJSON, _ := json.Marshal(srv.Args)
	headersJSON, _ := json.Marshal(srv.Headers)
	envJSON, _ := json.Marshal(srv.Env)
	enabled := 0
	if srv.Enabled {
		enabled = 1
	}
	logsEnabled := 0
	if srv.LogsEnabled {
		logsEnabled = 1
	}

	_, err := s.db.Exec(`
		INSERT INTO servers (id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, logs_enabled, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		srv.ID, srv.Name, srv.Transport, srv.Command, string(argsJSON),
		srv.URL, string(headersJSON), string(envJSON), srv.AuthToken,
		srv.Timeout, srv.ConnectTimeout, enabled, logsEnabled, srv.Status,
		srv.CreatedAt, srv.UpdatedAt,
	)
	return err
}

// GetServer retrieves a server by ID.
func (s *Store) GetServer(id string) (*models.Server, error) {
	row := s.db.QueryRow(`SELECT id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, logs_enabled, status, last_seen, created_at, updated_at FROM servers WHERE id = ?`, id)
	return scanServer(row)
}

// GetServerByName retrieves a server by name.
func (s *Store) GetServerByName(name string) (*models.Server, error) {
	row := s.db.QueryRow(`SELECT id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, logs_enabled, status, last_seen, created_at, updated_at FROM servers WHERE name = ?`, name)
	return scanServer(row)
}

// ListServers returns all servers.
func (s *Store) ListServers() ([]*models.Server, error) {
	rows, err := s.db.Query(`SELECT id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, logs_enabled, status, last_seen, created_at, updated_at FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*models.Server
	for rows.Next() {
		srv, err := scanServerRows(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, srv)
	}
	return servers, nil
}

// UpdateServer updates a server.
func (s *Store) UpdateServer(srv *models.Server) error {
	argsJSON, _ := json.Marshal(srv.Args)
	headersJSON, _ := json.Marshal(srv.Headers)
	envJSON, _ := json.Marshal(srv.Env)
	enabled := 0
	if srv.Enabled {
		enabled = 1
	}
	logsEnabled := 0
	if srv.LogsEnabled {
		logsEnabled = 1
	}

	_, err := s.db.Exec(`
		UPDATE servers SET
			name = ?, transport = ?, command = ?, args = ?, url = ?,
			headers = ?, env = ?, auth_token = ?, timeout = ?, connect_timeout = ?,
			enabled = ?, logs_enabled = ?, updated_at = ?
		WHERE id = ?
	`,
		srv.Name, srv.Transport, srv.Command, string(argsJSON),
		srv.URL, string(headersJSON), string(envJSON), srv.AuthToken,
		srv.Timeout, srv.ConnectTimeout, enabled, logsEnabled,
		time.Now(), srv.ID,
	)
	return err
}

// UpdateServerStatus updates only the status and last_seen fields.
func (s *Store) UpdateServerStatus(id, status string) error {
	var lastSeen interface{}
	if status == "connected" {
		lastSeen = time.Now()
	}
	_, err := s.db.Exec(`UPDATE servers SET status = ?, last_seen = ? WHERE id = ?`, status, lastSeen, id)
	return err
}

// DeleteServer removes a server.
func (s *Store) DeleteServer(id string) error {
	_, err := s.db.Exec(`DELETE FROM servers WHERE id = ?`, id)
	return err
}

// --- API Keys ---

// CreateAPIKey inserts a new API key.
func (s *Store) CreateAPIKey(key *models.APIKey) error {
	scopesJSON, _ := json.Marshal(key.Scopes)
	active := 0
	if key.Active {
		active = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO api_keys (id, name, key_hash, key_prefix, scopes, active, expires_at, created_at, compound_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		key.ID, key.Name, key.KeyHash, key.KeyPrefix,
		string(scopesJSON), active, key.ExpiresAt, key.CreatedAt, key.CompoundID,
	)
	return err
}

// GetAPIKeyByHash looks up an API key by its hash.
func (s *Store) GetAPIKeyByHash(hash string) (*models.APIKey, error) {
	row := s.db.QueryRow(`SELECT id, name, key_hash, key_prefix, scopes, active, last_used_at, expires_at, created_at, compound_id FROM api_keys WHERE key_hash = ? AND active = 1`, hash)
	return scanAPIKey(row)
}

// ListAPIKeys returns all API keys (without hashes).
func (s *Store) ListAPIKeys() ([]*models.APIKey, error) {
	rows, err := s.db.Query(`SELECT id, name, key_hash, key_prefix, scopes, active, last_used_at, expires_at, created_at, compound_id FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*models.APIKey
	for rows.Next() {
		key, err := scanAPIKeyRows(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// UpdateAPIKeyLastUsed updates the last_used_at timestamp.
func (s *Store) UpdateAPIKeyLastUsed(id string) error {
	_, err := s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, time.Now(), id)
	return err
}

// DeactivateAPIKey sets active to 0.
func (s *Store) DeactivateAPIKey(id string) error {
	_, err := s.db.Exec(`UPDATE api_keys SET active = 0 WHERE id = ?`, id)
	return err
}

// DeleteAPIKey removes an API key.
func (s *Store) DeleteAPIKey(id string) error {
	_, err := s.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// --- Users ---

// CreateUser inserts a new user.
func (s *Store) CreateUser(user *models.User) error {
	_, err := s.db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, user.Role, user.CreatedAt)
	return err
}

// GetUserByUsername retrieves a user by username.
func (s *Store) GetUserByUsername(username string) (*models.User, error) {
	row := s.db.QueryRow(`SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?`, username)
	var user models.User
	var createdAt string
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &createdAt)
	if err != nil {
		return nil, err
	}
	user.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &user, nil
}

// GetUserByOIDCSubject retrieves a user by their OIDC subject identifier.
func (s *Store) GetUserByOIDCSubject(subject string) (*models.User, error) {
	row := s.db.QueryRow(`SELECT id, username, password_hash, role, created_at FROM users WHERE oidc_subject = ?`, subject)
	var user models.User
	var createdAt string
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &createdAt)
	if err != nil {
		return nil, err
	}
	user.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &user, nil
}

// CreateUserFromOIDC creates a new user provisioned from an OIDC login.
func (s *Store) CreateUserFromOIDC(username, oidcSubject string) (*models.User, error) {
	user := &models.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: "", // no password — OIDC only
		Role:         "admin",
		CreatedAt:    time.Now(),
	}
	_, err := s.db.Exec(`INSERT INTO users (id, username, password_hash, role, oidc_subject, created_at) VALUES (?, ?, '', ?, ?, ?)`,
		user.ID, user.Username, user.Role, oidcSubject, user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// LinkOIDCSubject links an OIDC subject to an existing user.
func (s *Store) LinkOIDCSubject(userID, oidcSubject string) error {
	_, err := s.db.Exec(`UPDATE users SET oidc_subject = ? WHERE id = ?`, oidcSubject, userID)
	return err
}

// --- OAuth Tokens ---

// SaveOAuthTokens stores or updates OAuth tokens for a server.
func (s *Store) SaveOAuthTokens(serverID string, tokens *mcp.OAuthTokens, clientID, clientSecret string) error {
	var expiresAt interface{}
	if !tokens.ExpiresAt.IsZero() {
		expiresAt = tokens.ExpiresAt
	}
	_, err := s.db.Exec(`
		INSERT INTO oauth_tokens (server_id, access_token, token_type, refresh_token, expires_at, scope, client_id, client_secret, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET
			access_token = excluded.access_token,
			token_type = excluded.token_type,
			refresh_token = excluded.refresh_token,
			expires_at = excluded.expires_at,
			scope = excluded.scope,
			client_id = excluded.client_id,
			client_secret = excluded.client_secret,
			updated_at = excluded.updated_at
	`,
		serverID, tokens.AccessToken, tokens.TokenType, tokens.RefreshToken,
		expiresAt, tokens.Scope, clientID, clientSecret, time.Now(),
	)
	return err
}

// GetOAuthTokens retrieves stored OAuth tokens for a server.
func (s *Store) GetOAuthTokens(serverID string) (*mcp.OAuthTokens, string, string, error) {
	row := s.db.QueryRow(`SELECT access_token, token_type, refresh_token, expires_at, scope, client_id, client_secret FROM oauth_tokens WHERE server_id = ?`, serverID)
	var t mcp.OAuthTokens
	var clientID, clientSecret string
	var expiresAt sql.NullTime
	err := row.Scan(&t.AccessToken, &t.TokenType, &t.RefreshToken, &expiresAt, &t.Scope, &clientID, &clientSecret)
	if err != nil {
		return nil, "", "", err
	}
	if expiresAt.Valid {
		t.ExpiresAt = expiresAt.Time
	}
	return &t, clientID, clientSecret, nil
}

// DeleteOAuthTokens removes OAuth tokens for a server.
func (s *Store) DeleteOAuthTokens(serverID string) error {
	_, err := s.db.Exec(`DELETE FROM oauth_tokens WHERE server_id = ?`, serverID)
	return err
}

// --- OAuth Client Registration ---

// OAuthRegistration is a persisted dynamic client registration keyed by issuer.
type OAuthRegistration struct {
	Issuer                  string `json:"issuer"`
	ClientID                string `json:"client_id"`
	ClientSecret            string `json:"client_secret,omitempty"`
	RegistrationAccessToken string `json:"registration_access_token,omitempty"`
	ClientIDIssuedAt        int64  `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt   int64  `json:"client_secret_expires_at,omitempty"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
}

// GetOAuthRegistration retrieves a persisted client registration by issuer.
func (s *Store) GetOAuthRegistration(issuer string) (*OAuthRegistration, error) {
	row := s.db.QueryRow(`SELECT issuer, client_id, client_secret, registration_access_token, client_id_issued_at, client_secret_expires_at, created_at, updated_at FROM oauth_registrations WHERE issuer = ?`, issuer)
	var r OAuthRegistration
	if err := row.Scan(&r.Issuer, &r.ClientID, &r.ClientSecret, &r.RegistrationAccessToken, &r.ClientIDIssuedAt, &r.ClientSecretExpiresAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// SaveOAuthRegistration creates or updates a client registration keyed by issuer.
func (s *Store) SaveOAuthRegistration(reg *mcp.ClientRegistration, issuer string) error {
	_, err := s.db.Exec(`INSERT INTO oauth_registrations (issuer, client_id, client_secret, registration_access_token, client_id_issued_at, client_secret_expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(issuer) DO UPDATE SET client_id = excluded.client_id, client_secret = excluded.client_secret, registration_access_token = excluded.registration_access_token, client_id_issued_at = excluded.client_id_issued_at, client_secret_expires_at = excluded.client_secret_expires_at, updated_at = excluded.updated_at`,
		issuer, reg.ClientID, reg.ClientSecret, reg.RegistrationAccessToken, reg.ClientIDIssuedAt, reg.ClientSecretExpiresAt, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	return err
}

// DeleteOAuthRegistration removes a client registration by issuer.
func (s *Store) DeleteOAuthRegistration(issuer string) error {
	_, err := s.db.Exec(`DELETE FROM oauth_registrations WHERE issuer = ?`, issuer)
	return err
}

// --- Scanner helpers ---

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanServer(row *sql.Row) (*models.Server, error) {
	return scanServerImpl(row)
}

func scanServerRows(rows *sql.Rows) (*models.Server, error) {
	return scanServerImpl(rows)
}

func scanServerImpl(s rowScanner) (*models.Server, error) {
	var srv models.Server
	var argsJSON, headersJSON, envJSON string
	var enabled, logsEnabled int
	var lastSeen sql.NullTime
	var createdAt, updatedAt string

	err := s.Scan(
		&srv.ID, &srv.Name, &srv.Transport, &srv.Command, &argsJSON,
		&srv.URL, &headersJSON, &envJSON, &srv.AuthToken,
		&srv.Timeout, &srv.ConnectTimeout, &enabled, &logsEnabled, &srv.Status,
		&lastSeen, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	srv.Enabled = enabled == 1
	srv.LogsEnabled = logsEnabled == 1
	if lastSeen.Valid {
		srv.LastSeen = &lastSeen.Time
	}
	json.Unmarshal([]byte(argsJSON), &srv.Args)
	json.Unmarshal([]byte(headersJSON), &srv.Headers)
	json.Unmarshal([]byte(envJSON), &srv.Env)
	srv.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	srv.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)

	return &srv, nil
}

func scanAPIKey(row *sql.Row) (*models.APIKey, error) {
	return scanAPIKeyImpl(row)
}

func scanAPIKeyRows(rows *sql.Rows) (*models.APIKey, error) {
	return scanAPIKeyImpl(rows)
}

func scanAPIKeyImpl(s rowScanner) (*models.APIKey, error) {
	var key models.APIKey
	var scopesJSON string
	var active int
	var lastUsed sql.NullTime
	var expiresAt sql.NullTime
	var createdAt string
	var compoundID sql.NullString

	err := s.Scan(
		&key.ID, &key.Name, &key.KeyHash, &key.KeyPrefix,
		&scopesJSON, &active, &lastUsed, &expiresAt, &createdAt, &compoundID,
	)
	if err != nil {
		return nil, err
	}

	key.Active = active == 1
	if lastUsed.Valid {
		key.LastUsedAt = &lastUsed.Time
	}
	if expiresAt.Valid {
		key.ExpiresAt = &expiresAt.Time
	}
	if compoundID.Valid && compoundID.String != "" {
		cid := compoundID.String
		key.CompoundID = &cid
	}
	json.Unmarshal([]byte(scopesJSON), &key.Scopes)
	key.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)

	return &key, nil
}

// --- Compound Servers ---

// CreateCompound inserts a new compound server and optionally adds members.
func (s *Store) CreateCompound(c *models.CompoundServer, memberIDs []string) error {
	dictMode := 0
	if c.DictionaryMode {
		dictMode = 1
	}
	_, err := s.db.Exec(`INSERT INTO compound_servers (id, name, description, dictionary_mode, created_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Description, dictMode, c.CreatedAt)
	if err != nil {
		return err
	}
	for _, sid := range memberIDs {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO compound_members (compound_id, server_id) VALUES (?, ?)`, c.ID, sid); err != nil {
			return err
		}
	}
	return nil
}

// GetCompound retrieves a compound server by ID (without members).
func (s *Store) GetCompound(id string) (*models.CompoundServer, error) {
	row := s.db.QueryRow(`SELECT id, name, description, dictionary_mode, created_at FROM compound_servers WHERE id = ?`, id)
	var c models.CompoundServer
	var dictMode int
	var createdAt string
	if err := row.Scan(&c.ID, &c.Name, &c.Description, &dictMode, &createdAt); err != nil {
		return nil, err
	}
	c.DictionaryMode = dictMode == 1
	c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &c, nil
}

// ListCompounds returns all compound servers.
func (s *Store) ListCompounds() ([]*models.CompoundServer, error) {
	rows, err := s.db.Query(`SELECT id, name, description, dictionary_mode, created_at FROM compound_servers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var compounds []*models.CompoundServer
	for rows.Next() {
		var c models.CompoundServer
		var dictMode int
		var createdAt string
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &dictMode, &createdAt); err != nil {
			return nil, err
		}
		c.DictionaryMode = dictMode == 1
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		compounds = append(compounds, &c)
	}
	return compounds, nil
}

// GetCompoundMemberIDs returns the server IDs that are members of a compound.
func (s *Store) GetCompoundMemberIDs(compoundID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT server_id FROM compound_members WHERE compound_id = ?`, compoundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// AddCompoundMember adds a server to a compound.
func (s *Store) AddCompoundMember(compoundID, serverID string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO compound_members (compound_id, server_id) VALUES (?, ?)`, compoundID, serverID)
	return err
}

// RemoveCompoundMember removes a server from a compound.
func (s *Store) RemoveCompoundMember(compoundID, serverID string) error {
	_, err := s.db.Exec(`DELETE FROM compound_members WHERE compound_id = ? AND server_id = ?`, compoundID, serverID)
	return err
}

// UpdateCompound updates a compound server's name, description, and dictionary_mode.
func (s *Store) UpdateCompound(id string, req *models.UpdateCompoundRequest) error {
	if req.Name != nil {
		if _, err := s.db.Exec(`UPDATE compound_servers SET name = ? WHERE id = ?`, *req.Name, id); err != nil {
			return err
		}
	}
	if req.Description != nil {
		if _, err := s.db.Exec(`UPDATE compound_servers SET description = ? WHERE id = ?`, *req.Description, id); err != nil {
			return err
		}
	}
	if req.DictionaryMode != nil {
		dictMode := 0
		if *req.DictionaryMode {
			dictMode = 1
		}
		if _, err := s.db.Exec(`UPDATE compound_servers SET dictionary_mode = ? WHERE id = ?`, dictMode, id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteCompound removes a compound server and its members.
func (s *Store) DeleteCompound(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM compound_members WHERE compound_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM compound_servers WHERE id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	// Null out compound_id on api_keys that referenced this compound
	if _, err := tx.Exec(`UPDATE api_keys SET compound_id = NULL WHERE compound_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	// Remove per-compound disabled tool entries
	if _, err := tx.Exec(`DELETE FROM disabled_tools WHERE server_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// --- Memory Sets ---

// CreateMemorySet inserts a new memory set.
func (s *Store) CreateMemorySet(ms *models.MemorySet) error {
	isDefault := 0
	if ms.IsDefault {
		isDefault = 1
	}
	_, err := s.db.Exec(`INSERT INTO memory_sets (id, name, slug, description, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		ms.ID, ms.Name, ms.Slug, ms.Description, isDefault, ms.CreatedAt.Format("2006-01-02 15:04:05"))
	return err
}

// GetMemorySet retrieves a memory set by ID.
func (s *Store) GetMemorySet(id string) (*models.MemorySet, error) {
	row := s.db.QueryRow(`SELECT id, name, slug, description, is_default, created_at FROM memory_sets WHERE id = ?`, id)
	return scanMemorySet(row)
}

// ListMemorySets returns all memory sets.
func (s *Store) ListMemorySets() ([]*models.MemorySet, error) {
	rows, err := s.db.Query(`SELECT id, name, slug, description, is_default, created_at FROM memory_sets ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*models.MemorySet
	for rows.Next() {
		ms, err := scanMemorySetRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, ms)
	}
	return result, nil
}

// GetMemorySetBySlug retrieves a memory set by slug.
func (s *Store) GetMemorySetBySlug(slug string) (*models.MemorySet, error) {
	row := s.db.QueryRow(`SELECT id, name, slug, description, is_default, created_at FROM memory_sets WHERE slug = ?`, slug)
	return scanMemorySet(row)
}

// UpdateMemorySet updates a memory set's name and description.
func (s *Store) UpdateMemorySet(id string, req *models.UpdateMemorySetRequest) error {
	if req.Name != nil {
		if _, err := s.db.Exec(`UPDATE memory_sets SET name = ? WHERE id = ?`, *req.Name, id); err != nil {
			return err
		}
	}
	if req.Description != nil {
		if _, err := s.db.Exec(`UPDATE memory_sets SET description = ? WHERE id = ?`, *req.Description, id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteMemorySet removes a memory set. The default set can't be deleted.
func (s *Store) DeleteMemorySet(id string) error {
	if id == "default" {
		return fmt.Errorf("cannot delete the default memory set")
	}
	_, err := s.db.Exec(`DELETE FROM memory_sets WHERE id = ?`, id)
	return err
}

// DeleteMemoriesBySet removes all memories belonging to a given set.
func (s *Store) DeleteMemoriesBySet(setID string) error {
	_, err := s.db.Exec(`DELETE FROM memories WHERE set_id = ?`, setID)
	return err
}

func scanMemorySet(row *sql.Row) (*models.MemorySet, error) {
	return scanMemorySetImpl(row)
}

func scanMemorySetRows(rows *sql.Rows) (*models.MemorySet, error) {
	return scanMemorySetImpl(rows)
}

func scanMemorySetImpl(s rowScanner) (*models.MemorySet, error) {
	var ms models.MemorySet
	var isDefault int
	var createdAt string
	err := s.Scan(&ms.ID, &ms.Name, &ms.Slug, &ms.Description, &isDefault, &createdAt)
	if err != nil {
		return nil, err
	}
	ms.IsDefault = isDefault == 1
	ms.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &ms, nil
}

// --- Memories ---

// CreateMemory inserts a new memory.
func (s *Store) CreateMemory(mem *models.Memory) error {
	if mem.SetID == "" {
		mem.SetID = "default"
	}
	tagsJSON, _ := json.Marshal(mem.Tags)
	_, err := s.db.Exec(`
		INSERT INTO memories (id, set_id, palace, room, content, tags, importance, access_count, last_accessed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		mem.ID, mem.SetID, mem.Palace, mem.Room, mem.Content, string(tagsJSON),
		mem.Importance, mem.AccessCount, mem.LastAccessed,
		mem.CreatedAt, mem.UpdatedAt,
	)
	return err
}

// GetMemory retrieves a memory by ID.
func (s *Store) GetMemory(id string) (*models.Memory, error) {
	row := s.db.QueryRow(`SELECT id, set_id, palace, room, content, tags, importance, access_count, last_accessed, created_at, updated_at FROM memories WHERE id = ?`, id)
	return scanMemory(row)
}

// ListMemories retrieves memories, optionally filtered by set and palace.
func (s *Store) ListMemories(setID, palace string) ([]*models.Memory, error) {
	if setID == "" {
		setID = "default"
	}
	var rows *sql.Rows
	var err error
	if palace != "" {
		rows, err = s.db.Query(`SELECT id, set_id, palace, room, content, tags, importance, access_count, last_accessed, created_at, updated_at FROM memories WHERE set_id = ? AND palace = ? ORDER BY importance DESC, updated_at DESC`, setID, palace)
	} else {
		rows, err = s.db.Query(`SELECT id, set_id, palace, room, content, tags, importance, access_count, last_accessed, created_at, updated_at FROM memories WHERE set_id = ? ORDER BY importance DESC, updated_at DESC`, setID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// SearchMemories performs a full-text search on memory content and tags.
func (s *Store) SearchMemories(setID, query string) ([]*models.Memory, error) {
	if setID == "" {
		setID = "default"
	}
	likeQuery := "%" + query + "%"
	rows, err := s.db.Query(`SELECT id, set_id, palace, room, content, tags, importance, access_count, last_accessed, created_at, updated_at FROM memories WHERE set_id = ? AND (content LIKE ? OR tags LIKE ?) ORDER BY importance DESC, updated_at DESC`, setID, likeQuery, likeQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// ListPalaces returns distinct palace names with memory counts for a given set.
func (s *Store) ListPalaces(setID string) ([]map[string]interface{}, error) {
	if setID == "" {
		setID = "default"
	}
	rows, err := s.db.Query(`SELECT palace, COUNT(*) as cnt FROM memories WHERE set_id = ? GROUP BY palace ORDER BY palace`, setID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]interface{}
	for rows.Next() {
		var palace string
		var cnt int
		if err := rows.Scan(&palace, &cnt); err != nil {
			return nil, err
		}
		result = append(result, map[string]interface{}{"palace": palace, "count": cnt})
	}
	return result, nil
}

// UpdateMemory updates a memory's fields.
func (s *Store) UpdateMemory(id string, req *models.UpdateMemoryRequest) error {
	mem, err := s.GetMemory(id)
	if err != nil {
		return err
	}
	if req.Palace != nil {
		mem.Palace = *req.Palace
	}
	if req.Room != nil {
		mem.Room = *req.Room
	}
	if req.Content != nil {
		mem.Content = *req.Content
	}
	if req.Tags != nil {
		mem.Tags = *req.Tags
	}
	if req.Importance != nil {
		mem.Importance = *req.Importance
	}
	tagsJSON, _ := json.Marshal(mem.Tags)
	_, err = s.db.Exec(`UPDATE memories SET palace = ?, room = ?, content = ?, tags = ?, importance = ?, updated_at = ? WHERE id = ?`,
		mem.Palace, mem.Room, mem.Content, string(tagsJSON), mem.Importance, time.Now(), id)
	return err
}

// TouchMemory increments access count and updates last_accessed (hindsight-style).
func (s *Store) TouchMemory(id string) error {
	_, err := s.db.Exec(`UPDATE memories SET access_count = access_count + 1, last_accessed = ? WHERE id = ?`, time.Now(), id)
	return err
}

// DeleteMemory removes a memory.
func (s *Store) DeleteMemory(id string) error {
	_, err := s.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	return err
}

// CountMemories returns the total number of stored memories.
func (s *Store) CountMemories() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&count)
	return count, err
}

func scanMemory(row *sql.Row) (*models.Memory, error) {
	var mem models.Memory
	var tagsJSON string
	var lastAccessed sql.NullTime
	err := row.Scan(&mem.ID, &mem.SetID, &mem.Palace, &mem.Room, &mem.Content, &tagsJSON, &mem.Importance, &mem.AccessCount, &lastAccessed, &mem.CreatedAt, &mem.UpdatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(tagsJSON), &mem.Tags)
	if mem.Tags == nil {
		mem.Tags = []string{}
	}
	if lastAccessed.Valid {
		mem.LastAccessed = &lastAccessed.Time
	}
	return &mem, nil
}

func scanMemories(rows *sql.Rows) ([]*models.Memory, error) {
	var result []*models.Memory
	for rows.Next() {
		var mem models.Memory
		var tagsJSON string
		var lastAccessed sql.NullTime
		if err := rows.Scan(&mem.ID, &mem.SetID, &mem.Palace, &mem.Room, &mem.Content, &tagsJSON, &mem.Importance, &mem.AccessCount, &lastAccessed, &mem.CreatedAt, &mem.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tagsJSON), &mem.Tags)
		if mem.Tags == nil {
			mem.Tags = []string{}
		}
		if lastAccessed.Valid {
			mem.LastAccessed = &lastAccessed.Time
		}
		result = append(result, &mem)
	}
	return result, nil
}

// --- Env Vars ---

// CreateEnvVar inserts a new env var. The value field should already be
// encrypted by the handler before calling this method.
func (s *Store) CreateEnvVar(ev *models.EnvVar) error {
	_, err := s.db.Exec(`
		INSERT INTO env_vars (id, project, environment, key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		ev.ID, ev.Project, ev.Environment, ev.Key, ev.Value,
		ev.CreatedAt.Format(time.RFC3339), ev.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// GetEnvVar retrieves an env var by ID. The value field is encrypted at rest.
func (s *Store) GetEnvVar(id string) (*models.EnvVar, error) {
	row := s.db.QueryRow(`SELECT id, project, environment, key, value, created_at, updated_at FROM env_vars WHERE id = ?`, id)
	return scanEnvVar(row)
}

// ListEnvVars returns env vars, optionally filtered by project and/or environment.
func (s *Store) ListEnvVars(project, environment string) ([]*models.EnvVar, error) {
	query := `SELECT id, project, environment, key, value, created_at, updated_at FROM env_vars`
	var args []interface{}
	var conditions []string
	if project != "" {
		conditions = append(conditions, "project = ?")
		args = append(args, project)
	}
	if environment != "" {
		conditions = append(conditions, "environment = ?")
		args = append(args, environment)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY project, environment, key"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*models.EnvVar
	for rows.Next() {
		ev, err := scanEnvVarRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, ev)
	}
	return result, nil
}

// ListEnvVarProjects returns distinct project names from env_vars.
func (s *Store) ListEnvVarProjects() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT project FROM env_vars ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// ListEnvVarEnvironments returns distinct environments for a given project.
func (s *Store) ListEnvVarEnvironments(project string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT environment FROM env_vars WHERE project = ? ORDER BY environment`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var envs []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		envs = append(envs, e)
	}
	return envs, nil
}

// UpdateEnvVar updates an env var's value. The value should already be
// encrypted by the handler before calling this method.
func (s *Store) UpdateEnvVar(id string, req *models.UpdateEnvVarRequest) error {
	if req.Value == nil {
		return nil
	}
	_, err := s.db.Exec(`UPDATE env_vars SET value = ?, updated_at = ? WHERE id = ?`,
		*req.Value, time.Now().Format(time.RFC3339), id)
	return err
}

// DeleteEnvVar removes an env var by ID.
func (s *Store) DeleteEnvVar(id string) error {
	_, err := s.db.Exec(`DELETE FROM env_vars WHERE id = ?`, id)
	return err
}

// DeleteEnvVarsByProject removes all env vars for a given project.
func (s *Store) DeleteEnvVarsByProject(project string) error {
	_, err := s.db.Exec(`DELETE FROM env_vars WHERE project = ?`, project)
	return err
}

func scanEnvVar(row *sql.Row) (*models.EnvVar, error) {
	return scanEnvVarImpl(row)
}

func scanEnvVarRows(rows *sql.Rows) (*models.EnvVar, error) {
	return scanEnvVarImpl(rows)
}

func scanEnvVarImpl(s rowScanner) (*models.EnvVar, error) {
	var ev models.EnvVar
	var createdAt, updatedAt string
	err := s.Scan(&ev.ID, &ev.Project, &ev.Environment, &ev.Key, &ev.Value, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ev.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	ev.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &ev, nil
}

// --- Disabled Tools ---

// ListDisabledTools returns all disabled tools (global + per-compound).
func (s *Store) ListDisabledTools() ([]*models.DisabledTool, error) {
	rows, err := s.db.Query(`SELECT id, tool_name, server_id, created_at FROM disabled_tools ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDisabledTools(rows)
}

// ListDisabledToolsByCompound returns disabled tools scoped to a specific compound.
func (s *Store) ListDisabledToolsByCompound(compoundID string) ([]*models.DisabledTool, error) {
	rows, err := s.db.Query(`SELECT id, tool_name, server_id, created_at FROM disabled_tools WHERE server_id = ? ORDER BY created_at DESC`, compoundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDisabledTools(rows)
}

// CreateDisabledTool disables a tool. If serverID is nil the disable is global;
// otherwise it is scoped to the given compound (server_id).
func (s *Store) CreateDisabledTool(toolName string, serverID *string) (*models.DisabledTool, error) {
	// De-duplicate: if an entry already exists for this (tool_name, server_id),
	// return it instead of erroring. SQLite treats NULL as distinct in UNIQUE
	// constraints, so we check explicitly for the global case.
	var existingID string
	if serverID != nil {
		err := s.db.QueryRow(
			`SELECT id FROM disabled_tools WHERE tool_name = ? AND server_id = ?`,
			toolName, *serverID,
		).Scan(&existingID)
		if err == nil {
			return s.GetDisabledTool(existingID)
		}
	} else {
		err := s.db.QueryRow(
			`SELECT id FROM disabled_tools WHERE tool_name = ? AND server_id IS NULL`,
			toolName,
		).Scan(&existingID)
		if err == nil {
			return s.GetDisabledTool(existingID)
		}
	}

	dt := &models.DisabledTool{
		ID:        uuid.NewString(),
		ToolName:  toolName,
		ServerID:  serverID,
		CreatedAt: time.Now(),
	}

	var serverIDArg interface{}
	if serverID != nil {
		serverIDArg = *serverID
	}
	_, err := s.db.Exec(
		`INSERT INTO disabled_tools (id, tool_name, server_id, created_at) VALUES (?, ?, ?, ?)`,
		dt.ID, dt.ToolName, serverIDArg, dt.CreatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return dt, nil
}

// GetDisabledTool retrieves a single disabled tool entry by ID.
func (s *Store) GetDisabledTool(id string) (*models.DisabledTool, error) {
	row := s.db.QueryRow(`SELECT id, tool_name, server_id, created_at FROM disabled_tools WHERE id = ?`, id)
	return scanDisabledTool(row)
}

// DeleteDisabledTool removes a disabled tool entry (re-enables the tool).
func (s *Store) DeleteDisabledTool(id string) error {
	_, err := s.db.Exec(`DELETE FROM disabled_tools WHERE id = ?`, id)
	return err
}

// IsToolDisabled reports whether a tool is disabled. When compoundID is
// non-nil and non-empty, both global disables and compound-specific disables
// are checked. When compoundID is nil/empty, only global disables are checked.
func (s *Store) IsToolDisabled(toolName string, compoundID *string) (bool, error) {
	var count int
	if compoundID != nil && *compoundID != "" {
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM disabled_tools WHERE tool_name = ? AND (server_id IS NULL OR server_id = ?)`,
			toolName, *compoundID,
		).Scan(&count)
		return count > 0, err
	}
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM disabled_tools WHERE tool_name = ? AND server_id IS NULL`,
		toolName,
	).Scan(&count)
	return count > 0, err
}

func scanDisabledTool(s rowScanner) (*models.DisabledTool, error) {
	var dt models.DisabledTool
	var serverID sql.NullString
	var createdAt string
	if err := s.Scan(&dt.ID, &dt.ToolName, &serverID, &createdAt); err != nil {
		return nil, err
	}
	if serverID.Valid {
		sid := serverID.String
		dt.ServerID = &sid
	}
	dt.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &dt, nil
}

func scanDisabledTools(rows *sql.Rows) ([]*models.DisabledTool, error) {
	var result []*models.DisabledTool
	for rows.Next() {
		dt, err := scanDisabledTool(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, dt)
	}
	return result, nil
}
