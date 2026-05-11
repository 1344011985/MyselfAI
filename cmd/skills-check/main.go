package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/1344011985/MyselfAI/internal/skills"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := "data/test-skills.db"

	// 1. 打开测试 DB
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() {
		db.Close()
		// 7. 删除测试 DB
		os.Remove(dbPath)
		fmt.Println("\n[cleanup] data/test-skills.db deleted")
	}()

	// 2. 初始化 skills store 和 hub
	store, err := skills.NewSQLiteSkillStore(db)
	if err != nil {
		log.Fatalf("init skills store: %v", err)
	}
	hub := skills.NewHub(store)
	fmt.Println("[ok] skills store and hub initialised")

	// 3. 添加触发词 skill（新闻助手, triggers: 新闻,news）
	newsSkill := &skills.Skill{
		Name:        "新闻助手",
		Description: "新闻摘要专家",
		Prompt:      "你是一个专业的新闻摘要助手，擅长提炼关键信息。",
		Triggers:    []string{"新闻", "news"},
		Always:      false,
		Enabled:     true,
	}
	if err := store.Add(newsSkill); err != nil {
		log.Fatalf("add news skill: %v", err)
	}
	fmt.Printf("[ok] added skill: %s (id: %s, triggers: %s)\n",
		newsSkill.Name, newsSkill.ID, strings.Join(newsSkill.Triggers, ","))

	// 4. 添加 always skill（基础助手, always: true）
	baseSkill := &skills.Skill{
		Name:        "基础助手",
		Description: "基础行为准则",
		Prompt:      "请始终保持礼貌，用中文回复。",
		Always:      true,
		Enabled:     true,
	}
	if err := store.Add(baseSkill); err != nil {
		log.Fatalf("add base skill: %v", err)
	}
	fmt.Printf("[ok] added skill: %s (id: %s, always: true)\n", baseSkill.Name, baseSkill.ID)

	// 5. 验证 hub.Augment 三个 case
	basePrompt := "你是飞书机器人"

	type testCase struct {
		input    string
		wantNews bool
		wantBase bool
	}
	cases := []testCase{
		{"今天有什么新闻", true, true},
		{"你好", false, true},
		{"写代码", false, true},
	}

	fmt.Println("\n--- Augment 测试 ---")
	allPassed := true
	for _, tc := range cases {
		augmented := hub.Augment(basePrompt, tc.input)

		hasNews := strings.Contains(augmented, "新闻助手")
		hasBase := strings.Contains(augmented, "基础助手")

		newsOK := hasNews == tc.wantNews
		baseOK := hasBase == tc.wantBase

		status := "PASS"
		if !newsOK || !baseOK {
			status = "FAIL"
			allPassed = false
		}

		fmt.Printf("[%s] input=%q  新闻助手=%v(want %v)  基础助手=%v(want %v)\n",
			status, tc.input, hasNews, tc.wantNews, hasBase, tc.wantBase)
	}

	// 6. 打印汇总
	fmt.Println()
	if allPassed {
		fmt.Println("[result] 全部通过 ✓")
	} else {
		fmt.Println("[result] 有用例失败 ✗")
		os.Exit(1)
	}
}
