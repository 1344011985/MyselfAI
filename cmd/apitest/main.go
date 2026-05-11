package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/1344011985/MyselfAI/internal/browser"
	"github.com/1344011985/MyselfAI/internal/claude"
	"github.com/1344011985/MyselfAI/internal/codex"
	"github.com/1344011985/MyselfAI/internal/config"
	"github.com/1344011985/MyselfAI/internal/httpbridge"
	"github.com/1344011985/MyselfAI/internal/imageutil"
	loopruntime "github.com/1344011985/MyselfAI/internal/loop"
	"github.com/1344011985/MyselfAI/internal/memory"
	"github.com/1344011985/MyselfAI/internal/skills"
	"github.com/1344011985/MyselfAI/internal/taskqueue"
	"github.com/1344011985/MyselfAI/pkg/logger"
)

func main() {
	port := flag.Int("port", 18080, "HTTP listen port")
	configPath := flag.String("config", "", "path to myself-ai.json (default: auto-detect)")
	flag.Parse()

	cfgPath := *configPath
	if cfgPath == "" {
		var err error
		cfgPath, err = config.ConfigPath()
		if err != nil {
			slog.Error("failed to resolve config path", "err", err)
			os.Exit(1)
		}
	}

	cfg, err := config.LoadFrom(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	if cfg.Channel == "" {
		cfg.Channel = "feishu"
	}

	log := logger.New(cfg.LogLevel, cfg.ConfigDir)
	testDataDir := filepath.Join(cfg.ConfigDir, "data", fmt.Sprintf("apitest_%d", *port))
	if err := os.MkdirAll(testDataDir, 0755); err != nil {
		log.Error("failed to create test data directory", "err", err)
		os.Exit(1)
	}
	cfg.Memory.DBPath = filepath.Join(testDataDir, "bot.db")
	if strings.TrimSpace(cfg.Images.CacheDir) == "" {
		cfg.Images.CacheDir = filepath.Join(testDataDir, "images")
	}

	store, err := memory.NewSQLiteStore(cfg.Memory.DBPath)
	if err != nil {
		log.Error("failed to open memory store", "err", err)
		os.Exit(1)
	}
	defer store.Close() //nolint:errcheck

	runner := claude.New(cfg.Claude.BinPath, cfg.Claude.TimeoutSeconds)
	codexRunner := codex.New(cfg.Codex.BinPath, cfg.Codex.TimeoutSeconds, cfg.Codex.Sandbox, cfg.Codex.Workdir, cfg.Codex.Model)
	selector := claude.NewModelSelector(cfg)
	downloader, err := imageutil.New(cfg.Images.CacheDir, cfg.Images.MaxSizeMB)
	if err != nil {
		log.Warn("image support disabled", "err", err)
	}

	var skillsHub *skills.Hub
	if skillStore, err := skills.NewSQLiteSkillStore(store.DB()); err != nil {
		log.Warn("skills disabled", "err", err)
	} else {
		skillsHub = skills.NewHub(skillStore)
	}
	_ = skillsHub

	var browserMgr *browser.Manager
	browserCacheDir := filepath.Join(testDataDir, "browser_cache")
	if bm, err := browser.NewManager(browserCacheDir); err != nil {
		log.Warn("browser disabled", "err", err)
	} else {
		browserMgr = bm
		defer browserMgr.Close()
	}
	_ = browserMgr

	systemPrompt := "你是一个测试用的 AI 助手，由 Claude 驱动。"
	queue, err := taskqueue.New(store.DB(), store, runner, codexRunner, downloader, selector, systemPrompt, log, 1)
	if err != nil {
		log.Error("failed to init task queue", "err", err)
		os.Exit(1)
	}
	loopStore, err := loopruntime.NewStore(store.DB())
	if err != nil {
		log.Error("failed to init loop store", "err", err)
		os.Exit(1)
	}
	loopBrain := loopruntime.NewBrainStore(filepath.Join(testDataDir, "notes"))
	loopRunner := loopruntime.NewRunner(loopStore, queue, loopruntime.WithBrainStore(loopBrain))

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	server := httpbridge.NewWithLoop(addr, queue, loopStore, loopRunner, log)
	log.Info("apitest http bridge starting", "addr", addr, "db", cfg.Memory.DBPath)
	if err := server.Start(); err != nil && err != http.ErrServerClosed {
		log.Error("apitest server error", "err", err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
