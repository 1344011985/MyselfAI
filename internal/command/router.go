package command

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/1344011985/MyselfAI/internal/browser"
	"github.com/1344011985/MyselfAI/internal/claude"
	"github.com/1344011985/MyselfAI/internal/imageutil"
	"github.com/1344011985/MyselfAI/internal/loop"
	"github.com/1344011985/MyselfAI/internal/memory"
	"github.com/1344011985/MyselfAI/internal/newsearch"
	"github.com/1344011985/MyselfAI/internal/skills"
	"github.com/1344011985/MyselfAI/internal/taskqueue"
)

var modelSwitchPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:切换|换|改|设置|设定)(?:模型|model)?(?:为|到|成)\s*(haiku|sonnet|opus|auto)`),
	regexp.MustCompile(`(?i)(?:使用|用)\s*(haiku|sonnet|opus|auto)\s*(?:模型|model)?`),
	regexp.MustCompile(`(?i)(?:模型|model)\s*(?:切换|换|改|设置|设定)(?:为|到|成)?\s*(haiku|sonnet|opus|auto)`),
}

var modelDisplayNames = map[string]string{
	"haiku":  "Haiku (快速轻量)",
	"sonnet": "Sonnet (均衡)",
	"opus":   "Opus (最强)",
	"auto":   "自动选择",
}

type IncomingMessage struct {
	UserID     string
	GroupID    string
	Content    string
	ImageURLs  []string
	ProgressFn func(string)
}

type Handler interface {
	Handle(ctx context.Context, msg *IncomingMessage) (string, error)
}

type Logger interface {
	Error(msg string, args ...any)
	Info(msg string, args ...any)
}

type Router struct {
	handlers  map[string]Handler
	fallback  Handler
	store     memory.Store
	tasks     taskqueue.Queue
	loops     loop.Store
	brain     *loop.BrainStore
	skillsHub *skills.Hub
}

var (
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func NewRouter(store memory.Store, runner *claude.Runner, downloader *imageutil.Downloader, selector *claude.ModelSelector, systemPrompt string, logger Logger, hub *skills.Hub, browserMgr *browser.Manager, queue taskqueue.Queue, loopStore loop.Store, loopRunner *loop.Runner, brainStore *loop.BrainStore) *Router {
	askH := &askHandler{
		store:        store,
		runner:       runner,
		downloader:   downloader,
		selector:     selector,
		systemPrompt: systemPrompt,
		logger:       logger,
	}
	newsSearcher := newsearch.NewSearcher()
	r := &Router{
		handlers: map[string]Handler{
			"/ask":      askH,
			"/new":      &newHandler{store: store},
			"/remember": &rememberHandler{store: store},
			"/forget":   &forgetHandler{store: store},
			"/history":  &historyHandler{store: store},
			"/help":     &helpHandler{},
			"/version":  &versionHandler{},
			"/news":     &newsHandler{searcher: newsSearcher, logger: logger},
		},
		fallback:  askH,
		store:     store,
		tasks:     queue,
		loops:     loopStore,
		brain:     brainStore,
		skillsHub: hub,
	}

	if queue != nil {
		r.handlers["/tasks"] = &tasksHandler{queue: queue}
		r.handlers["/status"] = &statusHandler{queue: queue}
		r.handlers["/cancel"] = &cancelHandler{queue: queue}
		r.handlers["/stop"] = &cancelHandler{queue: queue}
		r.handlers["/verify"] = &verifyHandler{queue: queue}
	}
	if loopStore != nil {
		if loopRunner == nil {
			loopRunner = loop.NewRunner(loopStore, queue, loop.WithBrainStore(brainStore))
		}
		r.handlers["/loop"] = &loopHandler{store: loopStore, runner: loopRunner, memory: store}
		if brainStore != nil {
			r.handlers["/think"] = &thinkHandler{store: loopStore, brain: brainStore}
			r.handlers["/brain"] = &brainHandler{store: loopStore, brain: brainStore}
		}
	}
	if hub != nil {
		r.handlers["/skill"] = &skillHandler{store: hub.Store()}
	}
	if browserMgr != nil {
		r.handlers["/browse"] = &browseHandler{
			manager:  browserMgr,
			runner:   runner,
			selector: selector,
			store:    store,
			logger:   logger,
		}
	}
	return r
}

func (r *Router) Route(ctx context.Context, msg *IncomingMessage) (string, error) {
	if model, ok := r.detectModelSwitch(msg.Content); ok {
		if err := r.store.SetModelPreference(msg.UserID, model); err != nil {
			return "模型切换失败，请稍后重试。", nil
		}
		return fmt.Sprintf("已切换模型为 %s", modelDisplayNames[model]), nil
	}

	if executor, ok := r.detectExecutorSwitch(msg.Content); ok {
		if err := r.store.SetExecutorPreference(msg.UserID, executor); err != nil {
			return "执行器切换失败，请稍后重试。", nil
		}
		if err := r.store.DeleteSession(msg.UserID); err != nil {
			return "执行器已切换，但清理旧会话失败；建议发送 /new 后继续。", nil
		}
		names := map[string]string{"claude": "Claude Code", "codex": "Codex (OpenAI)", "kiro": "Kiro (Amazon)"}
		return fmt.Sprintf("已切换执行器为 %s，后续对话默认使用此执行器。", names[executor]), nil
	}

	if !strings.HasPrefix(msg.Content, "/") {
		h := r.fallback
		if r.skillsHub != nil {
			if askH, ok := h.(*askHandler); ok {
				augmented := r.skillsHub.Augment(askH.systemPrompt, msg.Content)
				h = askH.withSystemPrompt(augmented)
			}
		}
		return h.Handle(ctx, msg)
	}

	parts := strings.SplitN(msg.Content, " ", 2)
	cmd := strings.ToLower(parts[0])
	if cmd == "/ask" {
		h := r.handlers["/ask"]
		if r.skillsHub != nil {
			if askH, ok := h.(*askHandler); ok {
				content := msg.Content
				if len(parts) > 1 {
					content = parts[1]
				}
				augmented := r.skillsHub.Augment(askH.systemPrompt, content)
				h = askH.withSystemPrompt(augmented)
			}
		}
		return h.Handle(ctx, msg)
	}

	h, ok := r.handlers[cmd]
	if !ok {
		return fmt.Sprintf("未知指令 %q，输入 /help 查看可用指令", cmd), nil
	}
	return h.Handle(ctx, msg)
}

func (r *Router) Tasks() taskqueue.Queue {
	return r.tasks
}

// IsSyncCommand returns true for commands that should be handled synchronously
// (model/executor switches, simple slash commands) and should NOT be submitted to the task queue.
func (r *Router) IsSyncCommand(content string) bool {
	trimmed := strings.TrimSpace(content)
	if _, ok := r.detectExecutorSwitch(trimmed); ok {
		return true
	}
	if _, ok := r.detectModelSwitch(trimmed); ok {
		return true
	}
	// Bare slash commands with no arguments (e.g. /new, /help, /version, /forget, /tasks)
	lower := strings.ToLower(trimmed)
	if lower == "/loop" || strings.HasPrefix(lower, "/loop ") ||
		lower == "/think" || strings.HasPrefix(lower, "/think ") ||
		lower == "/brain" || strings.HasPrefix(lower, "/brain ") {
		return true
	}
	for _, bareCmd := range []string{"/new", "/help", "/version", "/forget", "/tasks", "/history"} {
		if lower == bareCmd {
			return true
		}
	}
	return false
}

// detectExecutorSwitch returns ("claude"|"codex", true) when the message is a bare executor
// switch command: /claude or /codex with no further arguments.
func (r *Router) detectExecutorSwitch(content string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(content))
	if trimmed == "/claude" {
		return "claude", true
	}
	if trimmed == "/codex" {
		return "codex", true
	}
	if trimmed == "/kiro" {
		return "kiro", true
	}
	return "", false
}

func (r *Router) detectModelSwitch(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	for _, pat := range modelSwitchPatterns {
		if matches := pat.FindStringSubmatch(trimmed); len(matches) >= 2 {
			model := strings.ToLower(matches[1])
			if _, ok := modelDisplayNames[model]; ok {
				return model, true
			}
		}
	}
	return "", false
}
