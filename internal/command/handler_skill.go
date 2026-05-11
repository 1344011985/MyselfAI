package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/1344011985/MyselfAI/internal/skills"
)

// skillHandler handles /skill commands.
type skillHandler struct {
	store skills.SkillStore
}

// Handle dispatches /skill subcommands.
func (h *skillHandler) Handle(_ context.Context, msg *IncomingMessage) (string, error) {
	args := strings.Fields(strings.TrimPrefix(msg.Content, "/skill"))
	if len(args) == 0 {
		return h.usage(), nil
	}

	sub := strings.ToLower(args[0])
	rest := args[1:]

	switch sub {
	case "list":
		return h.list()
	case "show":
		if len(rest) == 0 {
			return "用法：/skill show <id>", nil
		}
		return h.show(rest[0])
	case "enable":
		if len(rest) == 0 {
			return "用法：/skill enable <id>", nil
		}
		return h.setEnabled(rest[0], true)
	case "disable":
		if len(rest) == 0 {
			return "用法：/skill disable <id>", nil
		}
		return h.setEnabled(rest[0], false)
	case "delete":
		if len(rest) == 0 {
			return "用法：/skill delete <id>", nil
		}
		return h.delete(rest[0])
	case "add":
		// /skill add <name> | <prompt>
		// 用 | 分隔 name 和 prompt，triggers 可选用逗号分隔在 prompt 后附加
		// 格式：/skill add 新闻助手 | 你是新闻摘要专家... | trigger1,trigger2
		return h.add(rest)
	default:
		return fmt.Sprintf("未知子命令 %q\n\n%s", sub, h.usage()), nil
	}
}

func (h *skillHandler) usage() string {
	return `**Skills 管理**

/skill list                         — 列出所有 skill
/skill show <id>                    — 查看详情
/skill add <name> | <prompt>        — 添加 skill（name 和 prompt 用 | 分隔）
/skill add <name> | <prompt> | <触发词1,触发词2>  — 带触发词
/skill enable <id>                  — 启用
/skill disable <id>                 — 禁用
/skill delete <id>                  — 删除`
}

func (h *skillHandler) list() (string, error) {
	list, err := h.store.List()
	if err != nil {
		return "查询失败：" + err.Error(), nil
	}
	if len(list) == 0 {
		return "暂无 skill，用 /skill add 添加", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("共 %d 个 skill：\n\n", len(list)))
	for _, s := range list {
		status := "✅"
		if !s.Enabled {
			status = "❌"
		}
		always := ""
		if s.Always {
			always = " [always]"
		}
		sb.WriteString(fmt.Sprintf("%s **%s** (id: %s)%s\n%s\n\n",
			status, s.Name, s.ID, always, s.Description))
	}
	return strings.TrimSpace(sb.String()), nil
}

func (h *skillHandler) show(id string) (string, error) {
	s, err := h.store.Get(id)
	if err != nil {
		return "查询失败：" + err.Error(), nil
	}
	if s == nil {
		return fmt.Sprintf("找不到 skill id=%s", id), nil
	}
	triggers := "无"
	if len(s.Triggers) > 0 {
		triggers = strings.Join(s.Triggers, ", ")
	}
	return fmt.Sprintf(
		"**%s** (id: %s)\n描述：%s\n触发词：%s\nAlways：%v\n启用：%v\n\n**Prompt：**\n%s",
		s.Name, s.ID, s.Description, triggers, s.Always, s.Enabled, s.Prompt,
	), nil
}

func (h *skillHandler) setEnabled(id string, enabled bool) (string, error) {
	s, err := h.store.Get(id)
	if err != nil {
		return "查询失败：" + err.Error(), nil
	}
	if s == nil {
		return fmt.Sprintf("找不到 skill id=%s", id), nil
	}
	s.Enabled = enabled
	if err := h.store.Update(s); err != nil {
		return "更新失败：" + err.Error(), nil
	}
	state := "已启用"
	if !enabled {
		state = "已禁用"
	}
	return fmt.Sprintf("skill **%s** %s", s.Name, state), nil
}

func (h *skillHandler) delete(id string) (string, error) {
	s, err := h.store.Get(id)
	if err != nil {
		return "查询失败：" + err.Error(), nil
	}
	if s == nil {
		return fmt.Sprintf("找不到 skill id=%s", id), nil
	}
	if err := h.store.Delete(id); err != nil {
		return "删除失败：" + err.Error(), nil
	}
	return fmt.Sprintf("skill **%s** 已删除", s.Name), nil
}

func (h *skillHandler) add(args []string) (string, error) {
	// Rejoin args and split by |
	full := strings.Join(args, " ")
	parts := strings.SplitN(full, "|", 3)

	if len(parts) < 2 {
		return "用法：/skill add <name> | <prompt> [| trigger1,trigger2]", nil
	}

	name := strings.TrimSpace(parts[0])
	prompt := strings.TrimSpace(parts[1])
	if name == "" || prompt == "" {
		return "name 和 prompt 不能为空", nil
	}

	var triggers []string
	if len(parts) == 3 {
		for _, t := range strings.Split(parts[2], ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				triggers = append(triggers, t)
			}
		}
	}

	s := &skills.Skill{
		Name:        name,
		Description: name,
		Prompt:      prompt,
		Triggers:    triggers,
		Always:      len(triggers) == 0, // no triggers = always inject
		Enabled:     true,
	}
	if err := h.store.Add(s); err != nil {
		return "添加失败：" + err.Error(), nil
	}
	triggerDesc := "无触发词（always 模式）"
	if len(triggers) > 0 {
		triggerDesc = "触发词：" + strings.Join(triggers, ", ")
	}
	return fmt.Sprintf("✅ skill **%s** 已添加（id: %s）\n%s", s.Name, s.ID, triggerDesc), nil
}
