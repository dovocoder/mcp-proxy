package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/models"

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

	_, err := s.db.Exec(`
		INSERT INTO servers (id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		srv.ID, srv.Name, srv.Transport, srv.Command, string(argsJSON),
		srv.URL, string(headersJSON), string(envJSON), srv.AuthToken,
		srv.Timeout, srv.ConnectTimeout, enabled, srv.Status,
		srv.CreatedAt, srv.UpdatedAt,
	)
	return err
}

// GetServer retrieves a server by ID.
func (s *Store) GetServer(id string) (*models.Server, error) {
	row := s.db.QueryRow(`SELECT id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, status, last_seen, created_at, updated_at FROM servers WHERE id = ?`, id)
	return scanServer(row)
}

// GetServerByName retrieves a server by name.
func (s *Store) GetServerByName(name string) (*models.Server, error) {
	row := s.db.QueryRow(`SELECT id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, status, last_seen, created_at, updated_at FROM servers WHERE name = ?`, name)
	return scanServer(row)
}

// ListServers returns all servers.
func (s *Store) ListServers() ([]*models.Server, error) {
	rows, err := s.db.Query(`SELECT id, name, transport, command, args, url, headers, env, auth_token, timeout, connect_timeout, enabled, status, last_seen, created_at, updated_at FROM servers ORDER BY name`)
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

	_, err := s.db.Exec(`
		UPDATE servers SET
			name = ?, transport = ?, command = ?, args = ?, url = ?,
			headers = ?, env = ?, auth_token = ?, timeout = ?, connect_timeout = ?,
			enabled = ?, updated_at = ?
		WHERE id = ?
	`,
		srv.Name, srv.Transport, srv.Command, string(argsJSON),
		srv.URL, string(headersJSON), string(envJSON), srv.AuthToken,
		srv.Timeout, srv.ConnectTimeout, enabled,
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
	var enabled int
	var lastSeen sql.NullTime
	var createdAt, updatedAt string

	err := s.Scan(
		&srv.ID, &srv.Name, &srv.Transport, &srv.Command, &argsJSON,
		&srv.URL, &headersJSON, &envJSON, &srv.AuthToken,
		&srv.Timeout, &srv.ConnectTimeout, &enabled, &srv.Status,
		&lastSeen, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	srv.Enabled = enabled == 1
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
	_, err := s.db.Exec(`INSERT INTO compound_servers (id, name, description, created_at) VALUES (?, ?, ?, ?)`,
		c.ID, c.Name, c.Description, c.CreatedAt)
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
	row := s.db.QueryRow(`SELECT id, name, description, created_at FROM compound_servers WHERE id = ?`, id)
	var c models.CompoundServer
	var createdAt string
	if err := row.Scan(&c.ID, &c.Name, &c.Description, &createdAt); err != nil {
		return nil, err
	}
	c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return &c, nil
}

// ListCompounds returns all compound servers.
func (s *Store) ListCompounds() ([]*models.CompoundServer, error) {
	rows, err := s.db.Query(`SELECT id, name, description, created_at FROM compound_servers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var compounds []*models.CompoundServer
	for rows.Next() {
		var c models.CompoundServer
		var createdAt string
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &createdAt); err != nil {
			return nil, err
		}
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

// UpdateCompound updates a compound server's name and description.
func (s *Store) UpdateCompound(id string, name, description *string) error {
	if name != nil {
		if _, err := s.db.Exec(`UPDATE compound_servers SET name = ? WHERE id = ?`, *name, id); err != nil {
			return err
		}
	}
	if description != nil {
		if _, err := s.db.Exec(`UPDATE compound_servers SET description = ? WHERE id = ?`, *description, id); err != nil {
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
	return tx.Commit()
}
