package skills

import (
	"fmt"
	"log/slog"
	"strings"
)

// Hub matches skills to user input and augments system prompts.
type Hub struct {
	store SkillStore
}

// NewHub creates a Hub with the given SkillStore.
func NewHub(store SkillStore) *Hub {
	return &Hub{store: store}
}

// Store returns the underlying SkillStore (used by command handlers).
func (h *Hub) Store() SkillStore {
	return h.store
}

// Match returns enabled skills that match the input (keyword match or always=true).
// Case-insensitive. Returns empty slice (never nil) on any error.
func (h *Hub) Match(input string) (matched []*Skill) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills hub Match panic", "recover", r)
			matched = []*Skill{} // safe empty slice on panic
		}
	}()

	skills, err := h.store.ListEnabled()
	if err != nil {
		slog.Warn("skills hub: failed to list enabled skills", "err", err)
		return nil
	}

	lower := strings.ToLower(input)
	for _, s := range skills {
		if s.Always {
			matched = append(matched, s)
			continue
		}
		for _, trigger := range s.Triggers {
			if trigger != "" && strings.Contains(lower, strings.ToLower(trigger)) {
				matched = append(matched, s)
				break
			}
		}
	}
	return matched
}

// Augment appends matched skill prompts to the system prompt.
// If no skills match, returns systemPrompt unchanged.
func (h *Hub) Augment(systemPrompt, input string) (result string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills hub Augment panic", "recover", r)
			result = systemPrompt // safe fallback
		}
	}()

	matched := h.Match(input)
	if len(matched) == 0 {
		return systemPrompt
	}

	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n## Skills\n")
	for _, s := range matched {
		sb.WriteString(fmt.Sprintf("\n### %s\n%s\n", s.Name, s.Prompt))
	}
	return sb.String()
}
