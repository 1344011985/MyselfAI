package httpbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	loopruntime "github.com/1344011985/MyselfAI/internal/loop"
	"github.com/1344011985/MyselfAI/internal/taskqueue"
)

type Server struct {
	addr       string
	queue      taskqueue.Queue
	loopStore  loopruntime.Store
	loopRunner *loopruntime.Runner
	log        *slog.Logger
	http       *http.Server
}

type chatRequest struct {
	ChatID     string `json:"chat_id"`
	UserID     string `json:"user_id"`
	SenderName string `json:"sender_name"`
	Content    string `json:"content"`
	Executor   string `json:"executor"`

	// legacy compat
	User string `json:"user"`
	Msg  string `json:"msg"`
}

type chatResponse struct {
	OK     bool   `json:"ok,omitempty"`
	Reply  string `json:"reply,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

func New(addr string, queue taskqueue.Queue, log *slog.Logger) *Server {
	return NewWithLoop(addr, queue, nil, nil, log)
}

func NewWithLoop(addr string, queue taskqueue.Queue, loopStore loopruntime.Store, loopRunner *loopruntime.Runner, log *slog.Logger) *Server {
	mux := http.NewServeMux()
	s := &Server{addr: addr, queue: queue, loopStore: loopStore, loopRunner: loopRunner, log: log}
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/chat", s.handleChat)
	mux.HandleFunc("/task", s.handleTask)
	mux.HandleFunc("/cancel", s.handleCancel)
	if loopStore != nil {
		mux.HandleFunc("/loop", s.handleLoop)
		mux.HandleFunc("/loops", s.handleLoops)
		mux.HandleFunc("/loop/pause", s.handleLoopPause)
		mux.HandleFunc("/loop/resume", s.handleLoopResume)
		mux.HandleFunc("/loop/run", s.handleLoopRun)
	}
	s.http = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	s.log.Info("http bridge starting", "addr", s.addr)
	err := s.http.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			s.log.Error("panic in http bridge /chat", "panic", fmt.Sprintf("%v", rec))
			writeJSON(w, http.StatusInternalServerError, chatResponse{Error: fmt.Sprintf("internal panic: %v", rec)})
		}
	}()

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, chatResponse{Error: "method not allowed"})
		return
	}
	if s.queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, chatResponse{Error: "task queue not configured"})
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = strings.TrimSpace(req.User)
	}
	if userID == "" {
		userID = "http_bridge_user"
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		content = strings.TrimSpace(req.Msg)
	}
	if content == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "content/msg is required"})
		return
	}

	task, err := s.queue.Submit(r.Context(), taskqueue.SubmitRequest{
		UserID:          userID,
		GroupID:         strings.TrimSpace(req.ChatID),
		Content:         content,
		ContinueSession: false,
		Executor:        strings.TrimSpace(req.Executor),
	})
	if err != nil {
		s.log.Error("http bridge submit failed", "err", err, "user_id", userID)
		writeJSON(w, http.StatusInternalServerError, chatResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, chatResponse{OK: true, TaskID: task.ID, Status: string(task.Status)})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, chatResponse{Error: "method not allowed"})
		return
	}
	if s.queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, chatResponse{Error: "task queue not configured"})
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "id is required"})
		return
	}
	task, err := s.queue.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, chatResponse{Error: "task not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "task": task})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, chatResponse{Error: "method not allowed"})
		return
	}
	if s.queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, chatResponse{Error: "task queue not configured"})
		return
	}
	var body struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	body.TaskID = strings.TrimSpace(body.TaskID)
	if body.TaskID == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "task_id is required"})
		return
	}
	if err := s.queue.Cancel(body.TaskID); err != nil {
		writeJSON(w, http.StatusInternalServerError, chatResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type loopCreateRequest struct {
	UserID        string `json:"user_id"`
	ChatID        string `json:"chat_id"`
	Title         string `json:"title"`
	ProjectKey    string `json:"project_key"`
	Goal          string `json:"goal"`
	ScheduleExpr  string `json:"schedule_expr"`
	Timezone      string `json:"timezone"`
	Executor      string `json:"executor"`
	SafetyProfile string `json:"safety_profile"`
	PlanJSON      string `json:"plan_json"`
}

type loopIDRequest struct {
	UserID string `json:"user_id"`
	ID     string `json:"id"`
}

func (s *Server) handleLoop(w http.ResponseWriter, r *http.Request) {
	if s.loopStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, chatResponse{Error: "loop store not configured"})
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.handleLoopCreate(w, r)
	case http.MethodGet:
		s.handleLoopGet(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, chatResponse{Error: "method not allowed"})
	}
}

func (s *Server) handleLoopCreate(w http.ResponseWriter, r *http.Request) {
	var req loopCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	userID := defaultUserID(req.UserID)
	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "goal is required"})
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = shortenRunes(goal, 28)
	}
	scheduleExpr := strings.TrimSpace(req.ScheduleExpr)
	if scheduleExpr == "" {
		scheduleExpr = "manual"
	}
	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	safety := loopruntime.SafetyProfile(strings.TrimSpace(req.SafetyProfile))
	if safety == "" {
		safety = loopruntime.SafetyProfileConservative
	}

	schedule := &loopruntime.LoopSchedule{
		UserID:        userID,
		GroupID:       strings.TrimSpace(req.ChatID),
		Title:         title,
		ProjectKey:    strings.TrimSpace(req.ProjectKey),
		Goal:          goal,
		ScheduleExpr:  scheduleExpr,
		Timezone:      timezone,
		Executor:      strings.TrimSpace(req.Executor),
		SafetyProfile: safety,
		PlanJSON:      strings.TrimSpace(req.PlanJSON),
	}
	if err := s.loopStore.CreateSchedule(r.Context(), schedule); err != nil {
		writeJSON(w, http.StatusInternalServerError, chatResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "loop": schedule})
}

func (s *Server) handleLoopGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "id is required"})
		return
	}
	schedule, err := s.loopStore.GetSchedule(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, chatResponse{Error: "loop not found"})
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID != "" && schedule.UserID != userID {
		writeJSON(w, http.StatusForbidden, chatResponse{Error: "permission denied"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "loop": schedule})
}

func (s *Server) handleLoops(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, chatResponse{Error: "method not allowed"})
		return
	}
	userID := defaultUserID(r.URL.Query().Get("user_id"))
	loops, err := s.loopStore.ListSchedules(r.Context(), userID, 20)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, chatResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "loops": loops})
}

func (s *Server) handleLoopPause(w http.ResponseWriter, r *http.Request) {
	s.handleLoopStatusUpdate(w, r, loopruntime.ScheduleStatusPaused)
}

func (s *Server) handleLoopResume(w http.ResponseWriter, r *http.Request) {
	s.handleLoopStatusUpdate(w, r, loopruntime.ScheduleStatusActive)
}

func (s *Server) handleLoopStatusUpdate(w http.ResponseWriter, r *http.Request, status loopruntime.ScheduleStatus) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, chatResponse{Error: "method not allowed"})
		return
	}
	var req loopIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "id is required"})
		return
	}
	if err := s.requireLoopOwner(r.Context(), id, defaultUserID(req.UserID)); err != nil {
		writeJSON(w, http.StatusForbidden, chatResponse{Error: err.Error()})
		return
	}
	if err := s.loopStore.UpdateScheduleStatus(r.Context(), id, status); err != nil {
		writeJSON(w, http.StatusInternalServerError, chatResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (s *Server) handleLoopRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, chatResponse{Error: "method not allowed"})
		return
	}
	if s.loopRunner == nil {
		writeJSON(w, http.StatusServiceUnavailable, chatResponse{Error: "loop runner not configured"})
		return
	}
	var req loopIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "id is required"})
		return
	}
	run, task, err := s.loopRunner.RunManual(r.Context(), id, defaultUserID(req.UserID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, chatResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": run, "task": task})
}

func (s *Server) requireLoopOwner(ctx context.Context, id, userID string) error {
	schedule, err := s.loopStore.GetSchedule(ctx, id)
	if err != nil {
		return fmt.Errorf("loop not found")
	}
	if schedule.UserID != userID {
		return fmt.Errorf("permission denied")
	}
	return nil
}

func defaultUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "http_bridge_user"
	}
	return userID
}

func shortenRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
