# MyselfAI

`MyselfAI` 是一个 Go 实现的飞书/Lark AI 助手网关。它可以把飞书消息路由到 Claude Code、Codex、Kiro 兼容 CLI 等执行器，使用本地 SQLite 保存会话/任务/记忆，并支持受控的长期 loop 任务。

## 功能

- 飞书/Lark WebSocket 机器人。
- 异步任务队列：提交、查询、取消、完成回调。
- Claude Code / Codex / Kiro 兼容 CLI 执行器适配。
- SQLite 保存会话、历史、长期记忆、任务状态。
- 文件级 notes 注入，可从本地笔记目录读取上下文。
- 浏览器辅助命令，可抓取网页并交给模型分析。
- Loop Runtime：创建、暂停、恢复、手动触发、查看 run 和 runlog。
- Loop 进度和结束结果可主动回流到飞书。

## 前置要求

- Go 1.24+。
- 一个飞书/Lark 自建应用，启用机器人和 WebSocket 事件订阅。
- 机器能访问 `open.feishu.cn`。
- 至少安装并登录一个执行器 CLI：
  - Claude Code CLI：`claude`
  - Codex CLI：`codex`（可选）
  - Kiro 兼容 CLI：`kiro-cli`（可选）

执行器 CLI 必须在运行 bot 的同一个系统用户下可用并已完成登录。

## 克隆与构建

```bash
git clone https://github.com/1344011985/MyselfAI.git
cd MyselfAI

go build -o dist/myself-ai ./cmd/bot
```

带版本信息构建：

```bash
go build \
  -ldflags "-X main.GitCommit=$(git rev-parse --short HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o dist/myself-ai ./cmd/bot
```

## 飞书应用配置

1. 在 [open.feishu.cn](https://open.feishu.cn) 创建自建应用。
2. 启用机器人能力和 WebSocket 长连接事件订阅。
3. 订阅消息接收事件。
4. 将机器人添加到企业/群聊。
5. 按需开通权限：
   - `im:message`：读取和发送消息。
   - `im:message.reaction:write`：添加/删除消息表情回应。
   - `contact:user.base:readonly`：读取发送者展示名。
   - 如使用流式卡片，还需要 CardKit 相关权限。
6. 将 `App ID` 和 `App Secret` 写入配置文件。
7. 可选：如果需要群聊里精确识别 @ 机器人，配置 `feishu.bot_open_id`。

## 配置

默认配置路径：

| 平台 | 路径 |
| --- | --- |
| macOS | `~/.myself-ai/myself-ai.json` |
| Linux | `~/.myself-ai/myself-ai.json` |
| Windows | `%USERPROFILE%\.myself-ai\myself-ai.json` |

首次运行前先创建配置：

```bash
mkdir -p ~/.myself-ai
cp myself-ai.example.json ~/.myself-ai/myself-ai.json
```

然后编辑 `~/.myself-ai/myself-ai.json`，填入飞书应用凭据和执行器 CLI 路径。

关键字段：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `channel` | `feishu` | 启动通道。 |
| `feishu.app_id` | 必填 | 飞书应用 App ID。 |
| `feishu.app_secret` | 必填 | 飞书应用 App Secret，不要提交到仓库。 |
| `feishu.bot_open_id` | 空 | 可选，用于群聊 @ 机器人识别。 |
| `claude.bin_path` | `claude` | Claude Code CLI 路径。 |
| `claude.default_model` | `sonnet` | 默认 Claude 模型 key。 |
| `codex.bin_path` | `codex` | Codex CLI 路径。 |
| `codex.timeout_seconds` | `3600` | Codex 任务超时时间。 |
| `codex.sandbox` | `workspace-write` | Codex 沙箱模式。 |
| `kiro.bin_path` | `kiro-cli` | Kiro 兼容 CLI 路径。 |
| `kiro.model` | `claude-opus-4.7` | 默认 Kiro 执行器模型。 |
| `memory.db_path` | `~/.myself-ai/data/bot.db` | SQLite 数据库路径。 |
| `notes.dir` | `~/.myself-ai/notes` | 本地 notes 目录。 |
| `allowlist` | 所有用户 | 可选，限制可使用的飞书 open_id。 |
| `system_prompt` | 内置 | 覆盖默认系统提示词。 |

## 运行

```bash
./dist/myself-ai
```

指定配置文件：

```bash
./dist/myself-ai -config /path/to/myself-ai.json
```

默认运行数据目录：

```text
~/.myself-ai/
  myself-ai.json
  data/
    bot.db
  notes/
    MEMORY.md
    daily/
    projects/
  logs/
```

不要提交这个目录，里面会包含本地状态和私人数据。

## 可用命令

| 命令 | 说明 |
| --- | --- |
| `/ask <问题>` | 向默认执行器提问。 |
| `/new` | 开启新会话。 |
| `/remember <内容>` | 保存长期记忆。 |
| `/forget` | 清除长期记忆。 |
| `/history [n]` | 查看最近对话历史。 |
| `/tasks` | 查看最近异步任务。 |
| `/status <task_id>` | 查看任务状态。 |
| `/cancel <task_id>` | 取消任务。 |
| `/browse <url> [问题]` | 抓取网页并分析。 |
| `/skill <名称> [参数]` | 触发技能。 |
| `/brain ...` | 查看 loop brain。 |
| `/think <loop_id> <想法>` | 写入 loop brain inbox。 |
| `/loop create <目标>` | 创建受控 loop。 |
| `/loop list` | 查看 loop 列表。 |
| `/loop status <loop_id>` | 查看 loop 状态。 |
| `/loop runs <loop_id> [limit]` | 查看最近 run。 |
| `/loop runlog <run_id>` | 查看单次 run 日志。 |
| `/loop pause <loop_id>` | 暂停 loop。 |
| `/loop resume <loop_id>` | 恢复 loop。 |
| `/loop run <loop_id>` | 手动触发一次 loop run。 |
| `/version` | 查看版本。 |
| `/help` | 查看帮助。 |

也支持自然语言切换模型/执行器，例如 `use opus`、`use sonnet`、`/codex`、`/kiro`，前提是对应执行器已配置好。

## HTTP Bridge

本地会启动 HTTP Bridge，默认地址为 `127.0.0.1:9191`，可用于本地集成和健康检查。

```bash
curl http://127.0.0.1:9191/health
```

## 测试

```bash
go test ./...
```

## 开源协议

Apache License 2.0。

## 安全提醒

- 不要提交 `myself-ai.json`、`~/.myself-ai/`、SQLite DB、日志、截图、本地 notes。
- 飞书 App Secret 和执行器登录凭据必须留在仓库外。
- loop 涉及删除文件、部署、修改凭据等高风险动作时，应先人工确认。
