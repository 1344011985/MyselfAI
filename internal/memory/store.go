package memory

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// HistoryEntry represents a single conversation turn.
type HistoryEntry struct {
	Input     string
	Response  string
	CreatedAt time.Time
}

// UsageRecord represents token usage for a single API call.
type UsageRecord struct {
	UserID              string
	SessionID           string
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	TotalCostUSD        float64
	CreatedAt           time.Time
}

// UsageSummary aggregates usage statistics.
type UsageSummary struct {
	TotalRequests        int
	TotalInputTokens     int
	TotalOutputTokens    int
	TotalCacheReadTokens int
	TotalCostUSD         float64
	ByModel              map[string]*ModelUsage
}

// ModelUsage tracks usage for a specific model.
type ModelUsage struct {
	Model        string
	Requests     int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// Store defines the interface for all persistence operations.
type Store interface {
	// Session management
	GetSession(userID string) (sessionID string, err error)
	SaveSession(userID, sessionID string) error
	DeleteSession(userID string) error

	// User memories
	GetMemories(userID string) ([]string, error)
	AddMemory(userID, content string) error
	DeleteMemories(userID string) error

	// Conversation history
	SaveHistory(userID, input, response string) error
	GetHistory(userID string, n int) ([]HistoryEntry, error)

	// Model preferences
	GetModelPreference(userID string) (string, error)
	SetModelPreference(userID, model string) error

	// Executor preferences (claude / codex)
	GetExecutorPreference(userID string) (string, error)
	SetExecutorPreference(userID, executor string) error

	// Usage tracking
	RecordUsage(record *UsageRecord) error
	GetUsageSummary(userID string, startTime, endTime time.Time) (*UsageSummary, error)

	// Message dedup (persistent, survives restarts)
	CheckAndMarkSeen(messageID, accountID string) (alreadySeen bool, err error)
	PruneOldDedup(olderThanMinutes int) error

	// DB returns the underlying *sql.DB so other stores (e.g. skills) can share the connection.
	DB() *sql.DB

	Close() error
}

type sqliteStore struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    user_id    TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS memories (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_memories_user_id ON memories(user_id);

CREATE TABLE IF NOT EXISTS history (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT NOT NULL,
    input      TEXT NOT NULL,
    response   TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_history_user_id ON history(user_id);

CREATE TABLE IF NOT EXISTS user_model_preference (
    user_id          TEXT PRIMARY KEY,
    preferred_model  TEXT NOT NULL DEFAULT 'auto',
    updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_executor_preference (
    user_id           TEXT PRIMARY KEY,
    preferred_executor TEXT NOT NULL DEFAULT 'claude',
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS model_usage (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id                  TEXT NOT NULL,
    session_id               TEXT NOT NULL,
    model                    TEXT NOT NULL,
    input_tokens             INTEGER NOT NULL DEFAULT 0,
    output_tokens            INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens    INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens        INTEGER NOT NULL DEFAULT 0,
    total_cost_usd           REAL NOT NULL DEFAULT 0,
    created_at               DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_usage_user_time ON model_usage(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_model_time ON model_usage(model, created_at);

CREATE TABLE IF NOT EXISTS message_dedup (
    message_id TEXT NOT NULL,
    account_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (message_id, account_id)
);
`

// NewSQLiteStore opens (or creates) a SQLite database and initialises the schema.
func NewSQLiteStore(dbPath string) (Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &sqliteStore{db: db}, nil
}

// --- Session ---

func (s *sqliteStore) GetSession(userID string) (string, error) {
	var sessionID string
	err := s.db.QueryRow(
		`SELECT session_id FROM sessions WHERE user_id = ?`, userID,
	).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return sessionID, err
}

func (s *sqliteStore) SaveSession(userID, sessionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions(user_id, session_id, updated_at)
		 VALUES(?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET session_id=excluded.session_id, updated_at=excluded.updated_at`,
		userID, sessionID,
	)
	return err
}

func (s *sqliteStore) DeleteSession(userID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// --- Memories ---

func (s *sqliteStore) GetMemories(userID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT content FROM memories WHERE user_id = ? ORDER BY created_at ASC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		memories = append(memories, content)
	}
	return memories, rows.Err()
}

func (s *sqliteStore) AddMemory(userID, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO memories(user_id, content) VALUES(?, ?)`, userID, content,
	)
	return err
}

func (s *sqliteStore) DeleteMemories(userID string) error {
	_, err := s.db.Exec(`DELETE FROM memories WHERE user_id = ?`, userID)
	return err
}

// --- History ---

func (s *sqliteStore) SaveHistory(userID, input, response string) error {
	_, err := s.db.Exec(
		`INSERT INTO history(user_id, input, response) VALUES(?, ?, ?)`,
		userID, input, response,
	)
	return err
}

func (s *sqliteStore) GetHistory(userID string, n int) ([]HistoryEntry, error) {
	rows, err := s.db.Query(
		`SELECT input, response, created_at FROM history
		 WHERE user_id = ?
		 ORDER BY id DESC
		 LIMIT ?`,
		userID, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var ts string
		if err := rows.Scan(&e.Input, &e.Response, &ts); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ts)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- Message Dedup ---

// CheckAndMarkSeen atomically checks if a message was already seen and marks it.
// Returns true if the message was already seen (duplicate), false if it's new.
func (s *sqliteStore) CheckAndMarkSeen(messageID, accountID string) (bool, error) {
	res, err := s.db.Exec(
		"INSERT OR IGNORE INTO message_dedup(message_id, account_id) VALUES(?, ?)",
		messageID, accountID,
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	// rows == 0 means the row already existed (INSERT OR IGNORE skipped it) -> duplicate
	return rows == 0, nil
}

// PruneOldDedup removes dedup entries older than olderThanMinutes minutes.
func (s *sqliteStore) PruneOldDedup(olderThanMinutes int) error {
	_, err := s.db.Exec(
		"DELETE FROM message_dedup WHERE created_at < datetime('now', printf('-%d minutes', ?))",
		olderThanMinutes,
	)
	return err
}

// DB returns the underlying *sql.DB for sharing with other stores.
func (s *sqliteStore) DB() *sql.DB {
	return s.db
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

// --- Model Preferences ---

func (s *sqliteStore) GetModelPreference(userID string) (string, error) {
	var model string
	err := s.db.QueryRow(
		`SELECT preferred_model FROM user_model_preference WHERE user_id = ?`, userID,
	).Scan(&model)
	if err == sql.ErrNoRows {
		return "auto", nil // Default to auto
	}
	return model, err
}

func (s *sqliteStore) SetModelPreference(userID, model string) error {
	_, err := s.db.Exec(
		`INSERT INTO user_model_preference(user_id, preferred_model, updated_at)
		 VALUES(?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET preferred_model=excluded.preferred_model, updated_at=excluded.updated_at`,
		userID, model,
	)
	return err
}

// --- Executor Preferences ---

func (s *sqliteStore) GetExecutorPreference(userID string) (string, error) {
	var executor string
	err := s.db.QueryRow(
		`SELECT preferred_executor FROM user_executor_preference WHERE user_id = ?`, userID,
	).Scan(&executor)
	if err == sql.ErrNoRows {
		return "claude", nil
	}
	return executor, err
}

func (s *sqliteStore) SetExecutorPreference(userID, executor string) error {
	_, err := s.db.Exec(
		`INSERT INTO user_executor_preference(user_id, preferred_executor, updated_at)
		 VALUES(?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET preferred_executor=excluded.preferred_executor, updated_at=excluded.updated_at`,
		userID, executor,
	)
	return err
}

// --- Usage Tracking ---

func (s *sqliteStore) RecordUsage(record *UsageRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO model_usage(
			user_id, session_id, model,
			input_tokens, output_tokens,
			cache_creation_tokens, cache_read_tokens,
			total_cost_usd, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.UserID, record.SessionID, record.Model,
		record.InputTokens, record.OutputTokens,
		record.CacheCreationTokens, record.CacheReadTokens,
		record.TotalCostUSD, record.CreatedAt,
	)
	return err
}

func (s *sqliteStore) GetUsageSummary(userID string, startTime, endTime time.Time) (*UsageSummary, error) {
	query := `
		SELECT
			model,
			COUNT(*) as requests,
			SUM(input_tokens) as input_tokens,
			SUM(output_tokens) as output_tokens,
			SUM(cache_read_tokens) as cache_read_tokens,
			SUM(total_cost_usd) as cost
		FROM model_usage
		WHERE user_id = ? AND created_at >= ? AND created_at <= ?
		GROUP BY model
	`

	rows, err := s.db.Query(query, userID, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summary := &UsageSummary{
		ByModel: make(map[string]*ModelUsage),
	}

	for rows.Next() {
		var mu ModelUsage
		if err := rows.Scan(
			&mu.Model, &mu.Requests,
			&mu.InputTokens, &mu.OutputTokens,
			&summary.TotalCacheReadTokens,
			&mu.CostUSD,
		); err != nil {
			return nil, err
		}

		summary.TotalRequests += mu.Requests
		summary.TotalInputTokens += mu.InputTokens
		summary.TotalOutputTokens += mu.OutputTokens
		summary.TotalCostUSD += mu.CostUSD
		summary.ByModel[mu.Model] = &mu
	}

	return summary, rows.Err()
}
