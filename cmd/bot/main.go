package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/1344011985/MyselfAI/internal/browser"
	"github.com/1344011985/MyselfAI/internal/claude"
	"github.com/1344011985/MyselfAI/internal/codex"
	"github.com/1344011985/MyselfAI/internal/command"
	"github.com/1344011985/MyselfAI/internal/config"
	"github.com/1344011985/MyselfAI/internal/feishu"
	"github.com/1344011985/MyselfAI/internal/httpbridge"
	"github.com/1344011985/MyselfAI/internal/imageutil"
	"github.com/1344011985/MyselfAI/internal/kiro"
	loopruntime "github.com/1344011985/MyselfAI/internal/loop"
	"github.com/1344011985/MyselfAI/internal/memory"
	"github.com/1344011985/MyselfAI/internal/notes"
	"github.com/1344011985/MyselfAI/internal/skills"
	"github.com/1344011985/MyselfAI/internal/taskqueue"
	"github.com/1344011985/MyselfAI/pkg/logger"
)

var (
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	channelFlag := flag.String("channel", "", "override channel from config (e.g. feishu)")
	configFlag := flag.String("config", "", "override config file path")
	flag.Parse()

	command.GitCommit = GitCommit
	command.BuildDate = BuildDate

	cfgPath := *configFlag
	if cfgPath == "" {
		var err error
		cfgPath, err = config.ConfigPath()
		if err != nil {
			slog.Error("failed to resolve config path", "err", err)
			os.Exit(1)
		}
	}

	slog.Info("starting myself-ai", "platform", config.Platform(), "config_path", cfgPath)

	cfg, err := config.LoadFrom(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	if *channelFlag != "" {
		cfg.Channel = *channelFlag
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}

	log := logger.New(cfg.LogLevel, cfg.ConfigDir)
	log.Info("config loaded", "platform", config.Platform(), "config_path", cfgPath, "config_dir", cfg.ConfigDir, "channel", cfg.Channel)

	if err := os.MkdirAll(cfg.ConfigDir+"/data", 0755); err != nil {
		log.Error("failed to create data directory", "err", err)
		os.Exit(1)
	}

	store, err := memory.NewSQLiteStore(cfg.Memory.DBPath)
	if err != nil {
		log.Error("failed to open memory store", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Error("error closing memory store", "err", err)
		}
	}()

	runner := claude.New(cfg.Claude.BinPath, cfg.Claude.TimeoutSeconds)
	codexRunner := codex.New(cfg.Codex.BinPath, cfg.Codex.TimeoutSeconds, cfg.Codex.Sandbox, cfg.Codex.Workdir, cfg.Codex.Model)
	kiroRunner := kiro.New(cfg.Kiro.BinPath, cfg.Kiro.TimeoutSeconds, cfg.Kiro.Model)
	selector := claude.NewModelSelector(cfg)

	downloader, err := imageutil.New(cfg.Images.CacheDir, cfg.Images.MaxSizeMB)
	if err != nil {
		log.Error("failed to init image downloader", "err", err)
		os.Exit(1)
	}
	if downloader != nil {
		log.Info("image support enabled", "cache_dir", cfg.Images.CacheDir)
	}

	var skillsHub *skills.Hub
	if skillStore, err := skills.NewSQLiteSkillStore(store.DB()); err != nil {
		log.Warn("failed to init skills store, skills disabled", "err", err)
	} else {
		skillsHub = skills.NewHub(skillStore)
		log.Info("skills hub enabled")
	}

	var browserMgr *browser.Manager
	browserCacheDir := cfg.ConfigDir + "/data/browser_cache"
	if bm, err := browser.NewManager(browserCacheDir); err != nil {
		log.Warn("failed to init browser manager, /browse disabled", "err", err)
	} else {
		browserMgr = bm
		log.Info("browser manager enabled", "cache_dir", browserCacheDir)
		defer browserMgr.Close()
	}

	systemPrompt := buildSystemPrompt(cfg)

	// 初始化文件级笔记系统（长期记忆）
	notesDir := cfg.Notes.Dir
	notesStore, err := notes.New(notesDir)
	if err != nil {
		log.Warn("failed to init notes store, notes disabled", "err", err)
	} else {
		log.Info("notes store enabled", "dir", notesDir)
		notesSection := notesStore.BuildPromptSection()
		if notesSection != "" {
			systemPrompt = systemPrompt + "\n\n## 长期记忆与笔记\n" + notesSection
			log.Info("notes injected into system prompt", "notes_len", len(notesSection))
		}
	}
	_ = notesStore // 后续 Phase 2 会传给 taskqueue 做自动回写

	queue, err := taskqueue.New(store.DB(), store, runner, codexRunner, downloader, selector, systemPrompt, log, 2)
	if err != nil {
		log.Error("failed to init task queue", "err", err)
		os.Exit(1)
	}
	// Wire skills hub into task queue so every async task gets skill prompt augmentation.
	if skillsHub != nil {
		if sq, ok := queue.(interface {
			SetSkillsHub(taskqueue.SkillsAugmenter)
		}); ok {
			sq.SetSkillsHub(skillsHub)
		}
	}
	// Wire Kiro runner into task queue.
	if sq, ok := queue.(interface{ SetKiroRunner(*kiro.Runner) }); ok {
		sq.SetKiroRunner(kiroRunner)
	}
	loopStore, err := loopruntime.NewStore(store.DB())
	if err != nil {
		log.Error("failed to init loop store", "err", err)
		os.Exit(1)
	}
	log.Info("loop runtime store enabled")
	loopBrain := loopruntime.NewBrainStore(notesDir)
	loopRunnerOptions := []loopruntime.RunnerOption{loopruntime.WithBrainStore(loopBrain)}
	if cfg.Channel == "feishu" {
		if notifier := feishu.NewLoopNotifier(cfg, log); notifier != nil {
			loopRunnerOptions = append(loopRunnerOptions, loopruntime.WithNotifier(notifier))
			log.Info("loop notifier enabled", "channel", "feishu")
		}
	}
	loopRunner := loopruntime.NewRunner(loopStore, queue, loopRunnerOptions...)

	router := command.NewRouter(store, runner, downloader, selector, systemPrompt, log, skillsHub, browserMgr, queue, loopStore, loopRunner, loopBrain)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	loopScheduler := loopruntime.NewScheduler(loopStore, loopRunner, log)
	go loopScheduler.Start(ctx)

	bridge := httpbridge.NewWithLoop("127.0.0.1:9191", queue, loopStore, loopRunner, log)
	go func() {
		if err := bridge.Start(); err != nil {
			log.Error("http bridge exited with error", "err", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := bridge.Shutdown(shutdownCtx); err != nil {
			log.Warn("http bridge shutdown failed", "err", err)
		}
	}()

	log.Info("bot starting", "channel", cfg.Channel, "commit", GitCommit, "built", BuildDate)

	switch cfg.Channel {
	case "feishu":
		if err := feishu.Start(ctx, cfg, router, store, log); err != nil {
			log.Error("feishu bot exited with error", "err", err)
			os.Exit(1)
		}
	default:
		log.Error("unsupported channel", "channel", cfg.Channel)
		os.Exit(1)
	}
}

func buildSystemPrompt(cfg *config.Config) string {
	if cfg.SystemPrompt != "" {
		return cfg.SystemPrompt
	}
	return `你是一个运行在飞书机器人上的 AI 助手，由 Claude 驱动。

## 行为准则
- 用中文回复，除非用户明确要求其他语言
- 可以使用 Markdown 格式（标题、列表、代码块），飞书支持渲染
- 回复内容尽量清晰有条理，善用格式化提升可读性
- 直接给出核心答案，避免冗长的铺垫

## 安全边界（严格遵守）
- 禁止执行任何删除、格式化、清空数据的危险操作
- 禁止访问或泄露配置文件中的敏感信息（AppID、AppSecret 等）
- 禁止访问工作目录以外的系统文件
- 禁止安装软件、修改系统配置、创建系统级计划任务
- 如用户需要长期/定期自主任务，只能使用 Bot 内置 Loop Runtime（/loop），不得直接写 crontab、LaunchAgent、系统计划任务或外部调度器
- 如果用户要求执行危险操作，礼貌拒绝并说明原因

## Loop Runtime（受控自主任务）
你具备受控的 Loop Runtime 能力，用于把长期目标保存为 Bot 内部 SQLite schedule，并由 Bot runtime 负责后续触发、记录和审计。

可用命令：
- /loop create <目标> —— 创建受控长期 loop；当前会保存计划，不直接执行危险操作
- /loop list —— 查看当前用户的 loop
- /loop status <loop_id> —— 查看 loop 详情
- /loop runs <loop_id> [limit] —— 查看最近 run、错误归因、产物和 diff 摘要
- /loop runlog <run_id> —— 查看单次 run 的事件日志和诊断
- /loop pause <loop_id> —— 暂停 loop
- /loop resume <loop_id> —— 恢复 loop
- /loop run <loop_id> —— 手动触发一次 loop run，提交 parent task

Loop 使用边界：
- Loop 是 Bot 内置能力，不是系统 crontab。
- Loop 默认保守执行：先观察、规划、创建小步任务和记录事件。
- Loop run 失败会写入错误归因；网络、超时、限流类错误会自动退避重试，配置、鉴权、取消、需要审批类错误不会自动重试。
- Loop run 支持 Feishu 主动汇报；执行器输出的进度会由 runtime 节流后主动发回创建 loop 的会话。
- 删除文件、安装依赖、修改凭据、重启服务、部署发布等动作必须要求人工确认。
- 如果用户让你规划“自我迭代”“定期检查”“自动跟进”，优先建议使用 /loop，而不是让用户手动重复发消息。

## 笔记系统（长期记忆）
你有一个文件级笔记系统，这是你的长期记忆库。笔记目录通过配置指定，当前配置为：` + cfg.Notes.Dir + `

每次启动时，系统会自动注入 MEMORY.md、今日 daily、昨日 daily 的部分内容到下方「长期记忆与笔记」区域；但完整知识库仍以文件目录为准，需要时应主动读取对应文件。

目录结构：
- MEMORY.md：核心长期记忆，记录稳定身份、偏好、长期规则、重要事实
- README.md：知识库总索引，说明目录用途和维护规则
- daily/YYYY-MM-DD.md：每日流水笔记，记录当天对话、操作、验证结果和临时判断
- projects/：长期项目档案，记录项目定位、路径、启动方式、架构决策、当前状态
- research/：专题调研，记录方案对比、结论、验证过程和可复用经验
- archive/：归档资料或低频参考内容

维护规则：
- 值得长期记住的稳定事实 → 更新 MEMORY.md
- 每次对话的关键信息、操作结果、待办 → 追加到当日 daily 文件
- 项目相关的长期状态和决策 → 更新 projects/ 下对应文档
- 专题调研或方案分析 → 更新 research/ 下对应文档
- 归档资料或低频参考内容 → 更新 archive/ 下对应文档
- 对话结束前，主动判断是否有内容需要写入长期记忆或每日笔记

## 可用命令
- /ask <问题> —— 向 Claude 提问（续接上下文）
- /new —— 开启新对话，清除当前 session
- /remember <内容> —— 保存长期记忆
- /forget —— 清除所有长期记忆
- /history [n] —— 查看最近 n 条对话
- /tasks —— 查看最近任务
- /status <task_id> —— 查看任务状态
- /cancel <task_id> —— 取消任务
- /stop [task_id] —— 停止最近进行中的任务；带 ID 时停止指定任务
- /loop create/list/status/runs/runlog/pause/resume/run —— 管理受控长期 loop
- /think <loop_id> <想法> —— 写入 loop brain Inbox
- /brain list/show/inbox/path —— 查看 loop brain 仪表盘
- /news [关键词] —— 搜索最新新闻
- /help —— 显示帮助
- /version —— 显示版本信息
- /claude —— 切换到 Claude Code 执行器
- /codex —— 切换到 Codex (OpenAI) 执行器
- /kiro —— 切换到 Kiro (Amazon) 执行器
- 直接发消息等同于 /ask`
}
