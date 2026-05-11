# MyselfAI

[中文文档](README.zh.md)

`MyselfAI` is a Go-based Lark/Feishu bot gateway for AI coding assistants. It can route messages to Claude Code, Codex, or Kiro-style CLI executors, keep local SQLite conversation state, expose a local HTTP task bridge, and run controlled long-lived loops.

## Features

- Feishu/Lark WebSocket bot integration.
- Async task queue with status, cancellation, and completion callbacks.
- Executor adapters for Claude Code, Codex CLI, and Kiro CLI-compatible tools.
- SQLite-backed sessions, message history, long-term memories, and task records.
- File-based notes injection from a local notes directory.
- Browser helper commands for fetching pages and extracting context.
- Loop runtime: create, pause, resume, manually run, inspect runs, and view run logs.
- Proactive loop notifications back to Feishu.

## Requirements

- Go 1.24+.
- A Feishu/Lark custom app with WebSocket events enabled.
- Network access to `open.feishu.cn`.
- At least one authenticated executor CLI:
  - Claude Code CLI: `claude`
  - Codex CLI: `codex` (optional)
  - Kiro-compatible CLI: `kiro-cli` (optional)

The bot starts with Feishu by default. Executor CLIs must already be installed and authenticated in the same user account that runs the bot.

## Clone And Build

```bash
git clone https://github.com/1344011985/MyselfAI.git
cd MyselfAI

go build -o dist/myself-ai ./cmd/bot
```

Build with version metadata:

```bash
go build \
  -ldflags "-X main.GitCommit=$(git rev-parse --short HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o dist/myself-ai ./cmd/bot
```

## Feishu App Setup

1. Create a custom app at [open.feishu.cn](https://open.feishu.cn).
2. Enable bot capability and WebSocket event subscription.
3. Subscribe to message receive events.
4. Add the bot to your workspace.
5. Grant the permissions your usage needs:
   - `im:message` for reading and sending messages.
   - `im:message.reaction:write` for reactions.
   - `contact:user.base:readonly` for sender display names.
   - CardKit permissions if you want streaming cards.
6. Copy the app's `App ID` and `App Secret` into `myself-ai.json`.
7. Optional: set `feishu.bot_open_id` if you need precise group mention detection.

## Configuration

The default config path is platform-specific:

| Platform | Path |
| --- | --- |
| macOS | `~/.myself-ai/myself-ai.json` |
| Linux | `~/.myself-ai/myself-ai.json` |
| Windows | `%USERPROFILE%\.myself-ai\myself-ai.json` |

Create the directory and config file before running:

```bash
mkdir -p ~/.myself-ai
cp myself-ai.example.json ~/.myself-ai/myself-ai.json
```

Then edit `~/.myself-ai/myself-ai.json` and fill in your Feishu app credentials and executor CLI paths.

Important fields:

| Field | Default | Description |
| --- | --- | --- |
| `channel` | `feishu` | Runtime channel. |
| `feishu.app_id` | required | Feishu app ID. |
| `feishu.app_secret` | required | Feishu app secret. Keep it private. |
| `feishu.bot_open_id` | empty | Optional bot open_id for group mention detection. |
| `claude.bin_path` | `claude` | Claude Code CLI path. |
| `claude.default_model` | `sonnet` | Default Claude model key. |
| `codex.bin_path` | `codex` | Codex CLI path. |
| `codex.timeout_seconds` | `3600` | Codex task timeout. |
| `codex.sandbox` | `workspace-write` | Codex sandbox mode. |
| `kiro.bin_path` | `kiro-cli` | Kiro-compatible CLI path. |
| `kiro.model` | `claude-opus-4.7` | Default Kiro executor model. |
| `memory.db_path` | `~/.myself-ai/data/bot.db` | SQLite database path. |
| `notes.dir` | `~/.myself-ai/notes` | File notes directory. |
| `allowlist` | all users | Optional Feishu open_id allowlist. |
| `system_prompt` | built-in | Override default system prompt. |

## Run

```bash
./dist/myself-ai
```

Override config path:

```bash
./dist/myself-ai -config /path/to/myself-ai.json
```

The bot stores runtime data under the config directory by default:

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

Do not commit this directory. It contains local state and may contain private data.

## Commands

| Command | Description |
| --- | --- |
| `/ask <question>` | Ask the default executor. |
| `/new` | Start a new session. |
| `/remember <content>` | Save long-term memory. |
| `/forget` | Clear long-term memory. |
| `/history [n]` | Show recent conversation history. |
| `/tasks` | List recent async tasks. |
| `/status <task_id>` | Show task status. |
| `/cancel <task_id>` | Cancel a task. |
| `/browse <url> [question]` | Fetch and analyze a webpage. |
| `/skill <name> [args]` | Trigger a registered skill. |
| `/brain ...` | Inspect loop brain files. |
| `/think <loop_id> <note>` | Append a note to a loop brain inbox. |
| `/loop create <goal>` | Create a controlled loop. |
| `/loop list` | List loops. |
| `/loop status <loop_id>` | Show loop status. |
| `/loop runs <loop_id> [limit]` | Show recent loop runs. |
| `/loop runlog <run_id>` | Show a run log. |
| `/loop pause <loop_id>` | Pause a loop. |
| `/loop resume <loop_id>` | Resume a loop. |
| `/loop run <loop_id>` | Manually trigger a loop run. |
| `/version` | Show version info. |
| `/help` | Show command help. |

Natural-language model switching is supported, for example `use opus`, `use sonnet`, `/codex`, or `/kiro` depending on configured executors.

## HTTP Bridge

The bot also starts a local bridge on `127.0.0.1:9191` by default. It is intended for local integrations and health checks.

```bash
curl http://127.0.0.1:9191/health
```

## Tests

```bash
go test ./...
```

## License

Apache License 2.0.

## Security Notes

- Never commit `myself-ai.json`, `~/.myself-ai/`, SQLite DB files, logs, screenshots, or local notes.
- Keep Feishu app secrets and executor credentials outside the repository.
- Review loop goals carefully before allowing file deletion, deployment, credential changes, or other high-risk actions.
