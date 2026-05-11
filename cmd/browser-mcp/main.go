package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/1344011985/MyselfAI/internal/browser"
)

func main() {
	cacheDir := os.TempDir() + "/browser-mcp-cache"

	mgr, err := browser.NewManager(cacheDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create browser manager: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	s := server.NewMCPServer("browser-mcp", "1.0.0")

	// --- browser_navigate ---
	s.AddTool(
		mcp.NewTool("browser_navigate",
			mcp.WithDescription("Navigate to a URL and wait for page load"),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to navigate to")),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			url := mcp.ParseString(req, "url", "")
			if url == "" {
				return mcp.NewToolResultError("url is required"), nil
			}
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			if err := mgr.Navigate(uid, url); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("navigate failed: %v", err)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Navigated to %s", url)), nil
		},
	)

	// --- browser_snapshot ---
	s.AddTool(
		mcp.NewTool("browser_snapshot",
			mcp.WithDescription("Return aria snapshot of current page (structured element list with e1/e2 refs)"),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			nodes, err := mgr.AriaSnapshot(uid)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("snapshot failed: %v", err)), nil
			}
			return mcp.NewToolResultText(browser.FormatAriaSnapshot(nodes)), nil
		},
	)

	// --- browser_screenshot ---
	s.AddTool(
		mcp.NewTool("browser_screenshot",
			mcp.WithDescription("Take a screenshot of the current page, returns base64 PNG image"),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			path, err := mgr.Screenshot(uid)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("screenshot failed: %v", err)), nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("read screenshot file: %v", err)), nil
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			return mcp.NewToolResultImage("screenshot", encoded, "image/png"), nil
		},
	)

	// --- browser_click ---
	s.AddTool(
		mcp.NewTool("browser_click",
			mcp.WithDescription("Click an element identified by aria snapshot ref (e.g. e1, e2)"),
			mcp.WithString("ref", mcp.Required(), mcp.Description("Element ref from aria snapshot, e.g. e3")),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ref := mcp.ParseString(req, "ref", "")
			if ref == "" {
				return mcp.NewToolResultError("ref is required"), nil
			}
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			if err := mgr.ClickRef(uid, ref); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("click failed: %v", err)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Clicked %s", ref)), nil
		},
	)

	// --- browser_type ---
	s.AddTool(
		mcp.NewTool("browser_type",
			mcp.WithDescription("Type text into an element identified by aria snapshot ref"),
			mcp.WithString("ref", mcp.Required(), mcp.Description("Element ref from aria snapshot, e.g. e2")),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to type")),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ref := mcp.ParseString(req, "ref", "")
			text := mcp.ParseString(req, "text", "")
			if ref == "" {
				return mcp.NewToolResultError("ref is required"), nil
			}
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			if err := mgr.TypeRef(uid, ref, text); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("type failed: %v", err)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Typed into %s", ref)), nil
		},
	)

	// --- browser_get_text ---
	s.AddTool(
		mcp.NewTool("browser_get_text",
			mcp.WithDescription("Return visible text content of the current page (body.innerText)"),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			text, err := mgr.GetText(uid)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("get_text failed: %v", err)), nil
			}
			return mcp.NewToolResultText(text), nil
		},
	)

	// --- browser_eval ---
	s.AddTool(
		mcp.NewTool("browser_eval",
			mcp.WithDescription("Execute JavaScript in the page context and return string result"),
			mcp.WithString("js", mcp.Required(), mcp.Description("JavaScript expression or function body to evaluate")),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			js := mcp.ParseString(req, "js", "")
			if strings.TrimSpace(js) == "" {
				return mcp.NewToolResultError("js is required"), nil
			}
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			result, err := mgr.EvalJS(uid, js)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("eval failed: %v", err)), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// --- browser_scroll ---
	s.AddTool(
		mcp.NewTool("browser_scroll",
			mcp.WithDescription("Scroll the page up or down"),
			mcp.WithString("direction", mcp.Required(), mcp.Description("Scroll direction: 'down' or 'up'")),
			mcp.WithNumber("pixels", mcp.Description("Pixels to scroll (default: 500)")),
			mcp.WithString("user_id", mcp.Description("Browser session ID (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			direction := mcp.ParseString(req, "direction", "")
			if direction != "down" && direction != "up" {
				return mcp.NewToolResultError("direction must be 'down' or 'up'"), nil
			}
			pixels := int(mcp.ParseFloat64(req, "pixels", 500))
			if pixels <= 0 {
				pixels = 500
			}
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			var scrollErr error
			if direction == "down" {
				scrollErr = mgr.ScrollDown(uid, pixels)
			} else {
				scrollErr = mgr.ScrollUp(uid, pixels)
			}
			if scrollErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("scroll failed: %v", scrollErr)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Scrolled %s %d px", direction, pixels)), nil
		},
	)

	// --- browser_close_session ---
	s.AddTool(
		mcp.NewTool("browser_close_session",
			mcp.WithDescription("Close and clean up a browser session"),
			mcp.WithString("user_id", mcp.Description("Browser session ID to close (default: mcp_user)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			uid := mcp.ParseString(req, "user_id", "mcp_user")
			mgr.CloseSession(uid)
			return mcp.NewToolResultText(fmt.Sprintf("Session %s closed", uid)), nil
		},
	)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
