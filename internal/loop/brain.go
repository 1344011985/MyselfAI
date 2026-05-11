package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxBrainPromptBytes = 24000

type BrainStore struct {
	root string
}

func NewBrainStore(notesDir string) *BrainStore {
	notesDir = strings.TrimSpace(notesDir)
	if notesDir == "" {
		notesDir = "notes"
	}
	return &BrainStore{root: filepath.Join(notesDir, "loops")}
}

func (b *BrainStore) Path(schedule *LoopSchedule) string {
	if b == nil || schedule == nil {
		return ""
	}
	slug := slugify(schedule.Title)
	if slug == "" {
		slug = "loop"
	}
	return filepath.Join(b.root, slug+"-"+schedule.ID+".md")
}

func (b *BrainStore) Ensure(schedule *LoopSchedule) (string, error) {
	path := b.Path(schedule)
	if path == "" {
		return "", fmt.Errorf("brain path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return path, os.WriteFile(path, []byte(defaultBrain(schedule)), 0644)
}

func (b *BrainStore) Read(schedule *LoopSchedule) (string, string, error) {
	path, err := b.Ensure(schedule)
	if err != nil {
		return "", "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	return string(content), path, nil
}

func (b *BrainStore) Write(schedule *LoopSchedule, content string) (string, error) {
	path, err := b.Ensure(schedule)
	if err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return path, fmt.Errorf("brain content is empty")
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return path, os.WriteFile(path, []byte(content), 0644)
}

func (b *BrainStore) AppendInbox(schedule *LoopSchedule, author, text string) (string, error) {
	content, path, err := b.Read(schedule)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return path, fmt.Errorf("inbox text is empty")
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = "user"
	}
	entry := fmt.Sprintf("- %s %s: %s\n", time.Now().Format("2006-01-02 15:04:05"), author, oneLine(text))
	updated := insertInboxEntry(content, entry)
	return path, os.WriteFile(path, []byte(updated), 0644)
}

func ExtractBrainBlock(result string) (string, bool) {
	matches := brainBlockPattern.FindStringSubmatch(result)
	if len(matches) < 2 {
		return "", false
	}
	content := strings.TrimSpace(matches[1])
	return content, content != ""
}

func TrimBrainForPrompt(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= maxBrainPromptBytes {
		return content
	}
	return content[len(content)-maxBrainPromptBytes:]
}

var (
	brainBlockPattern = regexp.MustCompile("(?is)```brain\\.md\\s*\\n(.*?)\\n?```")
	inboxHeader       = regexp.MustCompile(`(?m)^## Inbox\s*$`)
	slugUnsafe        = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
)

func insertInboxEntry(content, entry string) string {
	loc := inboxHeader.FindStringIndex(content)
	if loc == nil {
		content = strings.TrimRight(content, "\n")
		return content + "\n\n## Inbox\n" + entry
	}
	insertAt := loc[1]
	if insertAt < len(content) && content[insertAt] == '\r' {
		insertAt++
	}
	if insertAt < len(content) && content[insertAt] == '\n' {
		insertAt++
	}
	return content[:insertAt] + entry + content[insertAt:]
}

func defaultBrain(schedule *LoopSchedule) string {
	now := time.Now().Format("2006-01-02 15:04:05")
	return fmt.Sprintf(`# %s

loop_id: %s
project_key: %s
created_at: %s

## Objective
%s

## Current State
- 刚创建，尚未形成稳定状态。

## Inbox

## Decisions

## Next
- 等待下一轮 loop run 判断。

## Recent Runs
`, empty(schedule.Title, "Loop Brain"), schedule.ID, empty(schedule.ProjectKey, "-"), now, schedule.Goal)
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugUnsafe.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-._")
	if len(value) > 48 {
		value = strings.Trim(value[:48], "-._")
	}
	return value
}

func oneLine(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}
