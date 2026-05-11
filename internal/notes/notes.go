package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store manages file-based long-term notes (MEMORY.md + daily/).
type Store struct {
	baseDir string // e.g. ~/.myself-ai/notes
}

// New creates a notes Store rooted at the given directory.
// Creates the directory structure if it doesn't exist.
func New(baseDir string) (*Store, error) {
	dailyDir := filepath.Join(baseDir, "daily")
	if err := os.MkdirAll(dailyDir, 0755); err != nil {
		return nil, fmt.Errorf("notes: create dir: %w", err)
	}
	return &Store{baseDir: baseDir}, nil
}

// ReadMemory reads MEMORY.md and returns its content.
// Returns empty string (no error) if file doesn't exist.
func (s *Store) ReadMemory() (string, error) {
	return s.readFile(filepath.Join(s.baseDir, "MEMORY.md"))
}

// ReadDaily reads the daily note for a given date (format: YYYY-MM-DD).
// Returns empty string (no error) if file doesn't exist.
func (s *Store) ReadDaily(date string) (string, error) {
	return s.readFile(filepath.Join(s.baseDir, "daily", date+".md"))
}

// ReadToday reads today's daily note.
func (s *Store) ReadToday() (string, error) {
	return s.ReadDaily(today())
}

// AppendDaily appends a timestamped entry to today's daily note.
func (s *Store) AppendDaily(entry string) error {
	date := today()
	path := filepath.Join(s.baseDir, "daily", date+".md")

	// If file doesn't exist, write header first
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header := fmt.Sprintf("# %s 日记\n\n", date)
		if err := os.WriteFile(path, []byte(header), 0644); err != nil {
			return fmt.Errorf("notes: create daily: %w", err)
		}
	}

	ts := time.Now().Format("15:04")
	line := fmt.Sprintf("\n## [%s] %s\n", ts, entry)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("notes: append daily: %w", err)
	}
	defer f.Close()

	_, err = f.WriteString(line)
	return err
}

// UpdateMemory overwrites MEMORY.md with new content.
func (s *Store) UpdateMemory(content string) error {
	path := filepath.Join(s.baseDir, "MEMORY.md")
	return os.WriteFile(path, []byte(content), 0644)
}

// BuildPromptSection builds the notes section to inject into system prompt.
// Includes MEMORY.md + today's daily + yesterday's daily (if exists).
func (s *Store) BuildPromptSection() string {
	var parts []string

	memory, _ := s.ReadMemory()
	if memory != "" {
		parts = append(parts, memory)
	}

	todayNote, _ := s.ReadToday()
	if todayNote != "" {
		parts = append(parts, "---\n## 今日笔记\n"+todayNote)
	}

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	yesterdayNote, _ := s.ReadDaily(yesterday)
	if yesterdayNote != "" {
		// 截取最后 1000 字符，避免注入过长
		runes := []rune(yesterdayNote)
		if len(runes) > 1000 {
			yesterdayNote = "...\n" + string(runes[len(runes)-1000:])
		}
		parts = append(parts, "---\n## 昨日笔记（尾部）\n"+yesterdayNote)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// BaseDir returns the notes directory path.
func (s *Store) BaseDir() string {
	return s.baseDir
}

func (s *Store) readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("notes: read %s: %w", path, err)
	}
	return string(data), nil
}

func today() string {
	return time.Now().Format("2006-01-02")
}
