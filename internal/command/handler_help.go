package command

import "context"

// --- /help handler ---

type helpHandler struct{}

func (h *helpHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	return `## 对话
/ask <问题>          — 提问（续接上下文）
/new                 — 开启新对话，清除 session
直接发消息            — 等同于 /ask

## 执行器切换（持久化，重启后保留）
/claude              — 切换到 Claude Code（Anthropic）
/codex               — 切换到 Codex（OpenAI）
/kiro                — 切换到 Kiro（Amazon）
/codex <任务>        — 单次走 Codex，不改默认执行器
/kiro <任务>         — 单次走 Kiro，不改默认执行器

## 模型控制
**Claude**：发送自然语言即可切换，持久化保存
  · 切换到 sonnet / 使用 opus / 换 haiku / auto 自动选
  · haiku（快速轻量）/ sonnet（均衡）/ opus（最强）/ auto（按任务自动）
**Codex**：固定 gpt-5.5，改配置文件 codex.model 可换
**Kiro**：默认 claude-opus-4.7，改配置文件 kiro.model 可换
  · 可选：claude-sonnet-4.6 / claude-opus-4.7 / deepseek-3.2 / auto 等

## 记忆
/remember <内容>     — 保存长期记忆，每次对话自动注入
/forget              — 清除所有长期记忆
/history [n]         — 查看最近 n 条对话（默认 5）

## 任务管理
/tasks               — 查看最近任务列表
/status <task_id>    — 查看任务状态和结果
/cancel <task_id>    — 取消进行中的任务
/stop [task_id]      — 停止最近进行中的任务；带 ID 时停止指定任务
/verify <task_id>    — 标记任务已人工验证

## Loop Runtime（试点）
/loop create <目标>   — 创建受控长期 loop（当前先保存计划，不自动执行）
/loop list           — 查看我的 loop
/loop status <id>    — 查看 loop 详情
/loop runs <id>      — 查看最近 run、错误归因、产物和 diff 摘要
/loop runlog <run>   — 查看单次 run 的事件日志和诊断
/loop pause <id>     — 暂停 loop
/loop resume <id>    — 恢复 loop
/loop run <id>       — 手动触发一次 loop run（提交 parent task）
/think <id> <想法>   — 写入 loop brain 的 Inbox
/brain show <id>     — 查看 loop brain
/brain inbox <id>    — 查看 loop brain Inbox

## Skills（关键词触发 prompt 注入，三个执行器通用）
/skill list                                    — 查看所有 skill
/skill add <名称> | <prompt> [| 触发词1,触发词2] — 添加（无触发词则每次注入）
/skill show/enable/disable/delete <id>         — 查看/启停/删除

## 工具
/news [关键词]       — 搜索最新新闻（不带关键词显示热点）
/browse <url> [指令] — 打开网页 AI 分析（含"截图"关键词可截图）

## 其他
/help                — 显示此帮助
/version             — 显示版本信息`, nil
}
