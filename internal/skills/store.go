package skills

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Skill represents a reusable prompt snippet that can be injected into the system prompt.
type Skill struct {
	ID          string
	Name        string
	Description string   // shown to the user in /skill list
	Prompt      string   // injected into system prompt when triggered
	Triggers    []string // keywords that trigger this skill (case-insensitive)
	Always      bool     // if true, always inject regardless of triggers
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SkillStore defines persistence operations for skills.
type SkillStore interface {
	Add(skill *Skill) error
	Update(skill *Skill) error
	Delete(id string) error
	Get(id string) (*Skill, error)
	List() ([]*Skill, error)
	ListEnabled() ([]*Skill, error)
}

const skillsSchema = `
CREATE TABLE IF NOT EXISTS skills (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    prompt      TEXT NOT NULL,
    triggers    TEXT NOT NULL DEFAULT '[]',
    always      INTEGER NOT NULL DEFAULT 0,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

type sqliteSkillStore struct {
	db *sql.DB
}

// NewSQLiteSkillStore initialises the skills schema on an existing *sql.DB.
// The caller (main.go) is responsible for opening the DB — this avoids duplicate driver registration.
// Pass the same *sql.DB used by memory.NewSQLiteStore so both share one connection.
func NewSQLiteSkillStore(db *sql.DB) (SkillStore, error) {
	if _, err := db.Exec(skillsSchema); err != nil {
		return nil, fmt.Errorf("init skills schema: %w", err)
	}
	return &sqliteSkillStore{db: db}, nil
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// fallback to timestamp-based id
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (s *sqliteSkillStore) Add(skill *Skill) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills store Add panic", "recover", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	if skill.ID == "" {
		skill.ID = newID()
	}
	triggers, _ := json.Marshal(skill.Triggers)
	now := time.Now()
	skill.CreatedAt = now
	skill.UpdatedAt = now
	_, err = s.db.Exec(
		`INSERT INTO skills(id,name,description,prompt,triggers,always,enabled,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		skill.ID, skill.Name, skill.Description, skill.Prompt,
		string(triggers), boolToInt(skill.Always), boolToInt(skill.Enabled),
		now, now,
	)
	return
}

func (s *sqliteSkillStore) Update(skill *Skill) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills store Update panic", "recover", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	triggers, _ := json.Marshal(skill.Triggers)
	skill.UpdatedAt = time.Now()
	_, err = s.db.Exec(
		`UPDATE skills SET name=?,description=?,prompt=?,triggers=?,always=?,enabled=?,updated_at=? WHERE id=?`,
		skill.Name, skill.Description, skill.Prompt,
		string(triggers), boolToInt(skill.Always), boolToInt(skill.Enabled),
		skill.UpdatedAt, skill.ID,
	)
	return
}

func (s *sqliteSkillStore) Delete(id string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills store Delete panic", "recover", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	_, err = s.db.Exec(`DELETE FROM skills WHERE id=?`, id)
	return
}

func (s *sqliteSkillStore) Get(id string) (_ *Skill, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills store Get panic", "recover", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	row := s.db.QueryRow(
		`SELECT id,name,description,prompt,triggers,always,enabled,created_at,updated_at FROM skills WHERE id=?`, id,
	)
	return scanSkill(row)
}

func (s *sqliteSkillStore) List() (_ []*Skill, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills store List panic", "recover", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	rows, err := s.db.Query(
		`SELECT id,name,description,prompt,triggers,always,enabled,created_at,updated_at FROM skills ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSkills(rows)
}

func (s *sqliteSkillStore) ListEnabled() (_ []*Skill, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills store ListEnabled panic", "recover", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	rows, err := s.db.Query(
		`SELECT id,name,description,prompt,triggers,always,enabled,created_at,updated_at FROM skills WHERE enabled=1 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSkills(rows)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSkill(row scanner) (*Skill, error) {
	var s Skill
	var triggersJSON string
	var alwaysInt, enabledInt int
	var createdAt, updatedAt string
	if err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Prompt,
		&triggersJSON, &alwaysInt, &enabledInt, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	s.Always = alwaysInt != 0
	s.Enabled = enabledInt != 0
	json.Unmarshal([]byte(triggersJSON), &s.Triggers) //nolint:errcheck
	s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	s.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return &s, nil
}

func scanSkills(rows *sql.Rows) ([]*Skill, error) {
	var skills []*Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		if s != nil {
			skills = append(skills, s)
		}
	}
	return skills, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
