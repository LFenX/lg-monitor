// Package license is lgmon's beta gate: a release built with mode "enforce" asks for a beta key before the
// panel, the API and MCP do anything, and keeps checking with the owner's license service that the key still
// stands. Builds without it (development, tests, ordinary releases) are never gated.
package license

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// mode and server are set at release time with -ldflags -X; see scripts/build.ps1.
var (
	mode   = ""
	server = "https://license.lfen.ink"
)

// publicKey checks licenses offline; its private half is a secret of the license service only.
const publicKey = "qLFiGP8JsEt2zALV7+XYwIFhqRjOm+gOXyN63VvF100="

const (
	checkEvery = 30 * time.Minute
	// checkSoon is how often opening a window may ask again, so a revoked key is noticed without waiting.
	checkSoon = 5 * time.Minute
)

// Enforced reports whether this build asks for a beta key.
func Enforced() bool { return mode == "enforce" }

// Server is where licenses are activated and checked.
func Server() string { return server }

var ErrRejected = errors.New("license rejected")

// Token is what a license says once its signature checks out.
type Token struct {
	Version int    `json:"v"`
	Key     string `json:"kid"`
	Device  string `json:"dev"`
	Label   string `json:"label"`
	Expires int64  `json:"exp"`
	Renew   int64  `json:"ren"`
	Issued  int64  `json:"iat"`
	// Audience is set only on things signed for someone else, such as a 留白 ticket; those are never licenses.
	Audience string `json:"aud,omitempty"`
}

// Admin reports a license the owner gave their own computer, which no key backs.
func (t Token) Admin() bool { return t.Key == "admin" }

// Parse checks a license's signature and that it was issued to this device.
func Parse(text, device string, key ed25519.PublicKey) (Token, error) {
	var token Token
	body, signature, ok := strings.Cut(strings.TrimSpace(text), ".")
	if !ok {
		return token, fmt.Errorf("许可格式无效：%w", ErrRejected)
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !ed25519.Verify(key, []byte(body), sig) {
		return token, fmt.Errorf("许可签名不对：%w", ErrRejected)
	}
	data, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(data, &token) != nil || token.Audience != "" {
		return token, fmt.Errorf("许可内容无效：%w", ErrRejected)
	}
	if token.Device != device {
		return token, fmt.Errorf("许可不是发给这台电脑的：%w", ErrRejected)
	}
	return token, nil
}

// Device is this installation as the license service knows it.
type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Client talks to the license service.
type Client interface {
	Post(ctx context.Context, path, session string, body any) (int, []byte, error)
	Get(ctx context.Context, path, session string) (int, []byte, error)
}

type HTTPClient struct {
	Base string
	HTTP *http.Client
}

func (c HTTPClient) do(ctx context.Context, method, path, session string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Base, "/")+path, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if session != "" {
		request.Header.Set("X-Admin-Session", session)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("连不上授权服务：%w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, data, err
}

func (c HTTPClient) Post(ctx context.Context, path, session string, body any) (int, []byte, error) {
	return c.do(ctx, http.MethodPost, path, session, body)
}

func (c HTTPClient) Get(ctx context.Context, path, session string) (int, []byte, error) {
	return c.do(ctx, http.MethodGet, path, session, nil)
}

// Status is what the panel shows about the gate.
type Status struct {
	Enforced  bool   `json:"enforced" jsonschema_description:"这个版本是否要求内测密钥。"`
	State     string `json:"state" jsonschema:"enum=off,enum=active,enum=missing,enum=expired,enum=revoked,enum=stale" jsonschema_description:"off 不需要；active 已激活；missing 还没输入；expired 到期；revoked 被收回；stale 太久没能和授权服务核对。"`
	Label     string `json:"label,omitempty" jsonschema_description:"密钥的名字。"`
	ExpiresAt int64  `json:"expiresAt,omitempty" jsonschema_description:"密钥到期时间，UTC Unix 毫秒；0 不过期。"`
	Admin     bool   `json:"admin,omitempty" jsonschema_description:"管理员自己的电脑。"`
	// Device tells the owner's console which of the listed computers is this one.
	Device  string `json:"device,omitempty" jsonschema_description:"这台电脑在授权服务里的设备 ID。"`
	Problem string `json:"problem,omitempty"`
}

// Manager keeps this computer's license and checks it with the service now and then.
type Manager struct {
	path    string
	device  func() Device
	client  Client
	now     func() time.Time
	key     ed25519.PublicKey
	enforce bool

	idOnce sync.Once
	id     string

	mu       sync.Mutex
	loaded   bool
	text     string
	verified bool
	valid    bool
	token    Token
	state    string
	checked  time.Time
	problem  string
}

type Options struct {
	// Home is lgmon's data home; the license and this device's ID live in its data folder.
	Home    string
	Name    func() string
	Version string
	Client  Client
	Now     func() time.Time
	// Enforce overrides the build's mode; tests use it.
	Enforce *bool
	// PublicKey overrides the built-in key; tests use it.
	PublicKey ed25519.PublicKey
}

func NewManager(opts Options) *Manager {
	enforce := Enforced()
	if opts.Enforce != nil {
		enforce = *opts.Enforce
	}
	key := opts.PublicKey
	if key == nil {
		raw, _ := base64.StdEncoding.DecodeString(publicKey)
		key = ed25519.PublicKey(raw)
	}
	client := opts.Client
	if client == nil {
		client = HTTPClient{Base: server}
	}
	m := &Manager{path: filepath.Join(opts.Home, "data", "license.json"), client: client, now: opts.Now, key: key, enforce: enforce}
	m.device = func() Device {
		name := ""
		if opts.Name != nil {
			name = opts.Name()
		}
		return Device{ID: m.deviceID(), Name: name, Version: opts.Version}
	}
	return m
}

type stored struct {
	Device  string `json:"device"`
	License string `json:"license,omitempty"`
}

func (m *Manager) read() stored {
	var value stored
	if data, err := os.ReadFile(m.path); err == nil {
		_ = json.Unmarshal(data, &value)
	}
	return value
}

func (m *Manager) write(value stored) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return fmt.Errorf("保存许可失败：%w", err)
	}
	temp := m.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return fmt.Errorf("保存许可失败：%w", err)
	}
	return os.Rename(temp, m.path)
}

// deviceID is made once per installation and kept with the license.
func (m *Manager) deviceID() string {
	m.idOnce.Do(func() {
		value := m.read()
		if value.Device == "" {
			random := make([]byte, 16)
			_, _ = rand.Read(random)
			value.Device = "dev-" + hex.EncodeToString(random)
			_ = m.write(value)
		}
		m.id = value.Device
	})
	return m.id
}

func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	m.text = m.read().License
	m.parse()
}

// parse checks the signature once per license; after that only the clock moves the state.
func (m *Manager) parse() {
	if !m.verified {
		m.verified, m.valid, m.token = true, false, Token{}
		if m.text != "" {
			token, err := Parse(m.text, m.deviceID(), m.key)
			if err != nil {
				m.problem = err.Error()
			} else {
				m.valid, m.token = true, token
			}
		}
	}
	m.state = "missing"
	if !m.valid {
		return
	}
	token := m.token
	m.state = "active"
	now := m.now().UnixMilli()
	switch {
	case token.Expires > 0 && now >= token.Expires:
		m.state = "expired"
	case now >= token.Renew:
		m.state = "stale"
	}
}

// Allowed reports whether lgmon may be used now.
func (m *Manager) Allowed() bool {
	if !m.enforce {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	m.parse()
	return m.state == "active"
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	m.parse()
	admin := m.valid && m.token.Admin()
	// Only a computer holding a license has an ID worth naming; asking must not mint one.
	device := ""
	if m.text != "" {
		device = m.deviceID()
	}
	if !m.enforce {
		// Nothing is gated, but a computer the owner already adopted is still theirs, and the console says so.
		return Status{State: "off", Admin: admin, Device: device}
	}
	return Status{Enforced: true, State: m.state, Label: m.token.Label, ExpiresAt: m.token.Expires, Admin: admin, Device: device, Problem: m.problem}
}

type fault struct {
	Error struct{ Code, Message string } `json:"error"`
}

func serviceError(status int, data []byte) error {
	var value fault
	if json.Unmarshal(data, &value) == nil && value.Error.Message != "" {
		return fmt.Errorf("%s：%w", value.Error.Message, ErrRejected)
	}
	return fmt.Errorf("授权服务回复 HTTP %d", status)
}

// Activate exchanges a beta key for this computer's license.
func (m *Manager) Activate(ctx context.Context, key string) (Status, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return m.Status(), fmt.Errorf("请输入内测密钥：%w", ErrRejected)
	}
	return m.exchange(ctx, "/v1/activate", "", map[string]any{"key": key, "device": m.device()})
}

// Adopt licenses the owner's own computer, with an admin session from Admin login.
func (m *Manager) Adopt(ctx context.Context, session string) (Status, error) {
	return m.exchange(ctx, "/v1/admin/adopt", session, map[string]any{"device": m.device()})
}

func (m *Manager) exchange(ctx context.Context, path, session string, body any) (Status, error) {
	status, data, err := m.client.Post(ctx, path, session, body)
	if err != nil {
		return m.Status(), err
	}
	if status != http.StatusOK {
		return m.Status(), serviceError(status, data)
	}
	var reply struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &reply); err != nil || reply.Token == "" {
		return m.Status(), fmt.Errorf("授权服务回复无效")
	}
	if err := m.keep(reply.Token); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

func (m *Manager) keep(text string) error {
	if _, err := Parse(text, m.deviceID(), m.key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	value := m.read()
	value.License = text
	if err := m.write(value); err != nil {
		return err
	}
	m.text, m.loaded, m.checked, m.problem, m.verified = text, true, m.now(), "", false
	m.parse()
	return nil
}

func (m *Manager) forget(problem string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value := m.read()
	value.License = ""
	_ = m.write(value)
	m.text, m.problem, m.checked, m.verified = "", problem, m.now(), false
	m.parse()
}

// Check renews the license with the service; soon asks again even if the last check was recent.
func (m *Manager) Check(ctx context.Context, soon bool) Status {
	if !m.enforce {
		return m.Status()
	}
	m.mu.Lock()
	m.load()
	text, last := m.text, m.checked
	m.mu.Unlock()
	wait := checkEvery
	if soon {
		wait = checkSoon
	}
	if text == "" || (!last.IsZero() && m.now().Sub(last) < wait) {
		return m.Status()
	}
	status, data, err := m.client.Post(ctx, "/v1/check", "", map[string]any{"token": text, "device": m.device()})
	if err != nil {
		// Offline: the license holds until it was due to be renewed.
		m.mu.Lock()
		m.checked, m.problem = m.now(), "暂时连不上授权服务，许可到期前会继续尝试"
		m.mu.Unlock()
		return m.Status()
	}
	var reply struct {
		State string `json:"state"`
		Token string `json:"token"`
	}
	if status == http.StatusOK && json.Unmarshal(data, &reply) == nil {
		switch reply.State {
		case "active":
			if reply.Token != "" && m.keep(reply.Token) == nil {
				return m.Status()
			}
		case "revoked":
			m.forget("这台电脑的内测资格已被收回")
			return m.Status()
		case "expired":
			m.forget("内测密钥已过期")
			return m.Status()
		}
	}
	if status == http.StatusBadRequest {
		m.forget("许可无效，请重新输入内测密钥")
	}
	return m.Status()
}

// Run checks every half hour until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	if !m.enforce {
		return
	}
	m.Check(ctx, false)
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Check(ctx, false)
		}
	}
}

// ErrNoLicense means this computer holds no license to ask the service anything with.
var ErrNoLicense = errors.New("这台电脑还没有内测许可")

// MarginTicket asks the service for a short ticket that signs this computer's person into 留白.
func (m *Manager) MarginTicket(ctx context.Context) (string, error) {
	m.mu.Lock()
	m.load()
	m.parse()
	text, valid := m.text, m.valid
	m.mu.Unlock()
	if !valid {
		return "", ErrNoLicense
	}
	status, data, err := m.client.Post(ctx, "/v1/margin", "", map[string]any{"token": text, "device": m.device()})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", serviceError(status, data)
	}
	var reply struct {
		Ticket string `json:"ticket"`
	}
	if json.Unmarshal(data, &reply) != nil || !ticketShape.MatchString(reply.Ticket) {
		return "", fmt.Errorf("授权服务回复无效")
	}
	return reply.Ticket, nil
}

// ticketShape is base64url, a dot, base64url: the only text that may follow 留白's entry address.
var ticketShape = regexp.MustCompile(`^[A-Za-z0-9_-]{16,1000}\.[A-Za-z0-9_-]{86}$`)

// MarginEntry is 留白's door for lgmon; the ticket rides in the fragment, so it never reaches a server log.
const MarginEntry = "https://margin.lfen.ink/lgmon#ticket="

// MarginURL is the entry address for a ticket, or "" for anything that is not one.
func MarginURL(ticket string) string {
	if !ticketShape.MatchString(ticket) {
		return ""
	}
	return MarginEntry + ticket
}

// IsMarginURL reports whether an address is 留白's entry with a ticket and nothing else.
func IsMarginURL(address string) bool {
	ticket, ok := strings.CutPrefix(address, MarginEntry)
	return ok && ticketShape.MatchString(ticket)
}

// ForgetAdmin drops this computer's admin license once the owner took it off the list.
func (m *Manager) ForgetAdmin(device string) {
	if device != m.deviceID() {
		return
	}
	m.mu.Lock()
	m.load()
	m.parse()
	admin := m.valid && m.token.Admin()
	m.mu.Unlock()
	if admin {
		m.forget("这台电脑不再是管理员设备")
	}
}

// Admin passes an owner console request to the service and returns its reply as it is.
func (m *Manager) Admin(ctx context.Context, method, path, session string, body any) (int, []byte, error) {
	if !strings.HasPrefix(path, "/v1/admin/") {
		return 0, nil, fmt.Errorf("管理接口路径无效")
	}
	if method == http.MethodGet {
		return m.client.Get(ctx, path, session)
	}
	return m.client.Post(ctx, path, session, body)
}
