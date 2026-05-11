package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	sessionTimeout  = 5 * time.Minute
	navigateTimeout = 30 * time.Second
)

// AriaNode represents a single interactive element extracted from the DOM.
type AriaNode struct {
	Ref      string // e1, e2, ...
	Role     string // button, link, input, textarea, select, checkbox, ...
	Name     string // accessible name / text
	Value    string // current value (for inputs)
	Selector string // JS path used internally
}

// Session holds a single user's browser page.
type Session struct {
	page     *rod.Page
	refMap   map[string]string // ref -> JS selector
	lastUsed time.Time
	mu       sync.Mutex
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func (s *Session) close() {
	_ = s.page.Close()
}

// Manager owns a single rod.Browser and manages per-user pages.
type Manager struct {
	browser  *rod.Browser
	sessions map[string]*Session
	mu       sync.Mutex
	cacheDir string
	stopGC   chan struct{}
}

// NewManager creates a Manager. cacheDir stores screenshots.
func NewManager(cacheDir string) (*Manager, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("create browser cache dir: %w", err)
	}

	u, err := launcher.New().
		Headless(true).
		Leakless(false).
		Set("no-sandbox").
		Set("disable-gpu").
		Set("disable-dev-shm-usage").
		Set("lang", "zh-CN").
		Launch()
	if err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}

	b := rod.New().ControlURL(u).MustConnect()

	m := &Manager{
		browser:  b,
		sessions: make(map[string]*Session),
		cacheDir: cacheDir,
		stopGC:   make(chan struct{}),
	}
	go m.gcLoop()
	return m, nil
}

// Close shuts down all sessions and the underlying browser.
func (m *Manager) Close() {
	close(m.stopGC)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		s.close()
		delete(m.sessions, id)
	}
	_ = m.browser.Close()
}

func (m *Manager) gcLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.evictIdle()
		case <-m.stopGC:
			return
		}
	}
}

func (m *Manager) evictIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, s := range m.sessions {
		s.mu.Lock()
		idle := now.Sub(s.lastUsed)
		s.mu.Unlock()
		if idle > sessionTimeout {
			s.close()
			delete(m.sessions, id)
		}
	}
}

func (m *Manager) getOrCreate(userID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[userID]; ok {
		s.touch()
		return s, nil
	}

	page, err := m.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: 1280, Height: 800,
	}); err != nil {
		return nil, fmt.Errorf("set viewport: %w", err)
	}

	s := &Session{
		page:     page,
		refMap:   make(map[string]string),
		lastUsed: time.Now(),
	}
	m.sessions[userID] = s
	return s, nil
}

// Navigate opens url and waits for the page to load.
func (m *Manager) Navigate(userID, url string) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	defer s.touch()

	if err := s.page.Navigate(url); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}
	return s.page.WaitLoad()
}

// Screenshot takes a full-page screenshot and saves it to cacheDir.
func (m *Manager) Screenshot(userID string) (string, error) {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return "", err
	}
	defer s.touch()

	quality := 90
	buf, err := s.page.Screenshot(true, &proto.PageCaptureScreenshot{
		Format:  proto.PageCaptureScreenshotFormatPng,
		Quality: &quality,
	})
	if err != nil {
		return "", fmt.Errorf("screenshot: %w", err)
	}

	name := fmt.Sprintf("screenshot_%d.png", time.Now().UnixNano())
	path := filepath.Join(m.cacheDir, name)
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return "", fmt.Errorf("save screenshot: %w", err)
	}
	return path, nil
}

// GetText returns visible body text.
func (m *Manager) GetText(userID string) (string, error) {
	return m.EvalJS(userID, `document.body.innerText`)
}

// GetPageSource returns the full outer HTML.
func (m *Manager) GetPageSource(userID string) (string, error) {
	return m.EvalJS(userID, `document.documentElement.outerHTML`)
}

// GetCurrentURL returns the current page URL.
func (m *Manager) GetCurrentURL(userID string) (string, error) {
	return m.EvalJS(userID, `location.href`)
}

// EvalJS evaluates JS expression and returns the string result.
// js should be a function body like "() => document.body.innerText" or an expression.
func (m *Manager) EvalJS(userID, js string) (string, error) {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return "", err
	}
	defer s.touch()

	// go-rod Eval expects a function: wrap bare expressions automatically
	expr := js
	if !strings.HasPrefix(strings.TrimSpace(js), "()") && !strings.HasPrefix(strings.TrimSpace(js), "function") {
		expr = "() => { return " + js + " }"
	}
	res, err := s.page.Eval(expr)
	if err != nil {
		return "", fmt.Errorf("eval js: %w", err)
	}
	v := res.Value.String()
	if v == "null" || v == "undefined" {
		return "", nil
	}
	return v, nil
}

// --- Aria Snapshot ---

// ariaJS is the JS injected to extract interactive elements from the DOM.
const ariaJS = `() => {
	const roles = ["button","link","input","textarea","select","checkbox","radio","menuitem","tab","option","combobox","listbox","slider","spinbutton","switch","textbox","searchbox"];
	const elements = [];
	const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT);
	let node;
	while ((node = walker.nextNode())) {
		const tag = node.tagName.toLowerCase();
		const role = node.getAttribute("role") || "";
		const isInteractive = ["a","button","input","textarea","select"].includes(tag) || roles.includes(role);
		if (!isInteractive) continue;
		const rect = node.getBoundingClientRect();
		if (rect.width === 0 && rect.height === 0) continue;
		const name = (node.getAttribute("aria-label") || node.getAttribute("placeholder") || node.getAttribute("title") || node.textContent || "").trim().slice(0, 80);
		const value = node.value !== undefined ? String(node.value) : "";
		// build a simple unique selector: tag + index among same-tag siblings
		let idx = 0;
		let sib = node;
		while ((sib = sib.previousElementSibling)) { if (sib.tagName === node.tagName) idx++; }
		// Use xpath-style path
		const path = (() => {
			const parts = [];
			let el = node;
			while (el && el !== document.body) {
				let i = 1, s = el;
				while ((s = s.previousElementSibling)) i++;
				parts.unshift(el.tagName.toLowerCase() + ":nth-of-type(" + i + ")");
				el = el.parentElement;
			}
			return "body > " + parts.join(" > ");
		})();
		elements.push({ tag, role: role || tag, name, value, path });
	}
	return JSON.stringify(elements);
}`

// AriaSnapshot extracts interactive elements, assigns e1/e2... refs, returns list.
func (m *Manager) AriaSnapshot(userID string) ([]AriaNode, error) {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return nil, err
	}
	defer s.touch()

	res, err := s.page.Eval(ariaJS)
	if err != nil {
		return nil, fmt.Errorf("aria snapshot eval: %w", err)
	}

	// The JS returns JSON.stringify(elements) so res.Value is a JSON string.
	// We need to extract the string then json.Unmarshal it into the struct slice.
	jsonStr := res.Value.String()
	// go-rod may wrap the string in quotes; strip them if needed.
	if len(jsonStr) >= 2 && jsonStr[0] == '"' {
		if unquoted, err := strconv.Unquote(jsonStr); err == nil {
			jsonStr = unquoted
		}
	}
	var raw []struct {
		Tag   string `json:"tag"`
		Role  string `json:"role"`
		Name  string `json:"name"`
		Value string `json:"value"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("aria snapshot unmarshal: %w", err)
	}

	// reset refMap for this user
	s.mu.Lock()
	s.refMap = make(map[string]string)
	s.mu.Unlock()

	nodes := make([]AriaNode, 0, len(raw))
	for i, item := range raw {
		ref := fmt.Sprintf("e%d", i+1)
		s.mu.Lock()
		s.refMap[ref] = item.Path
		s.mu.Unlock()
		nodes = append(nodes, AriaNode{
			Ref:      ref,
			Role:     item.Role,
			Name:     item.Name,
			Value:    item.Value,
			Selector: item.Path,
		})
	}
	return nodes, nil
}

// FormatAriaSnapshot formats nodes as a concise text tree for AI/human consumption.
func FormatAriaSnapshot(nodes []AriaNode) string {
	var sb strings.Builder
	for _, n := range nodes {
		line := fmt.Sprintf("[%s] %s", n.Ref, n.Role)
		if n.Name != "" {
			line += ` "` + n.Name + `"`
		}
		if n.Value != "" {
			line += ` = "` + n.Value + `"`
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

// ClickRef clicks the element identified by a ref (e.g., "e3").
func (m *Manager) ClickRef(userID, ref string) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	selector, ok := s.refMap[ref]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("ref %s not found — run AriaSnapshot first", ref)
	}
	defer s.touch()

	el, err := s.page.Element(selector)
	if err != nil {
		return fmt.Errorf("find element %s: %w", ref, err)
	}
	return el.Click(proto.InputMouseButtonLeft, 1)
}

// TypeRef clears and types text into the element identified by ref.
func (m *Manager) TypeRef(userID, ref, text string) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	selector, ok := s.refMap[ref]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("ref %s not found — run AriaSnapshot first", ref)
	}
	defer s.touch()

	el, err := s.page.Element(selector)
	if err != nil {
		return fmt.Errorf("find element %s: %w", ref, err)
	}
	if err := el.SelectAllText(); err != nil {
		return fmt.Errorf("select all text in %s: %w", ref, err)
	}
	return el.Input(text)
}

// CloseSession forcibly removes a user's session.
func (m *Manager) CloseSession(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[userID]; ok {
		s.close()
		delete(m.sessions, userID)
	}
}

// WaitForSelector waits until selector is visible (max 15s).
func (m *Manager) WaitForSelector(userID, selector string) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	defer s.touch()
	el, err := s.page.Timeout(15 * time.Second).Element(selector)
	if err != nil {
		return fmt.Errorf("element %q not found: %w", selector, err)
	}
	return el.WaitVisible()
}

// WaitForLoad waits for network idle or a timeout.
func (m *Manager) WaitForLoad(userID string) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	defer s.touch()
	return s.page.WaitIdle(30 * time.Second)
}

// ScrollDown scrolls the page down by pixels.
func (m *Manager) ScrollDown(userID string, pixels int) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	defer s.touch()
	return s.page.Mouse.Scroll(0, float64(pixels), 10)
}

// ScrollUp scrolls the page up by pixels.
func (m *Manager) ScrollUp(userID string, pixels int) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	defer s.touch()
	return s.page.Mouse.Scroll(0, float64(-pixels), 10)
}

// keyByName maps common key names to input.Key constants.
var keyByName = map[string]input.Key{
	"Enter":      input.Enter,
	"Tab":        input.Tab,
	"Escape":     input.Escape,
	"Esc":        input.Escape,
	"Space":      input.Space,
	"Backspace":  input.Backspace,
	"Delete":     input.Delete,
	"ArrowUp":    input.ArrowUp,
	"ArrowDown":  input.ArrowDown,
	"ArrowLeft":  input.ArrowLeft,
	"ArrowRight": input.ArrowRight,
}

// PressKey sends a keyboard key press (e.g. "Enter", "Tab", "Escape").
func (m *Manager) PressKey(userID, key string) error {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return err
	}
	defer s.touch()
	k, ok := keyByName[key]
	if !ok {
		return fmt.Errorf("unknown key %q (supported: Enter, Tab, Escape, Space, Backspace, Delete, ArrowUp, ArrowDown, ArrowLeft, ArrowRight)", key)
	}
	return s.page.Keyboard.Press(k)
}

// SaveState persists cookies to a JSON file in cacheDir.
func (m *Manager) SaveState(userID, name string) (string, error) {
	s, err := m.getOrCreate(userID)
	if err != nil {
		return "", err
	}
	defer s.touch()

	cookies, err := s.page.Cookies(nil)
	if err != nil {
		return "", fmt.Errorf("get cookies: %w", err)
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return "", fmt.Errorf("marshal cookies: %w", err)
	}
	path := filepath.Join(m.cacheDir, "state_"+name+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write state file: %w", err)
	}
	return path, nil
}

// LoadState restores cookies from a previously saved state file.
func (m *Manager) LoadState(userID, name string) error {
	path := filepath.Join(m.cacheDir, "state_"+name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read state file: %w", err)
	}
	var cookies []*proto.NetworkCookieParam
	if err := json.Unmarshal(data, &cookies); err != nil {
		return fmt.Errorf("unmarshal cookies: %w", err)
	}
	return m.browser.SetCookies(cookies)
}
