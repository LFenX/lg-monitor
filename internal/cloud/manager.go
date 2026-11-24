// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/jobs"
)

// defaultServer may be overridden at build time with -ldflags -X github.com/LFenX/lg-monitor/internal/cloud.defaultServer=...
var defaultServer = "https://sync.lfen.ink"

const (
	sessionSecretKey = "cloud/session"
	// The secret store refuses empty values, so signing out overwrites the token with this marker.
	signedOutMarker = "signed-out"
	heartbeatEvery  = 60 * time.Second
	// Uptime and latency drift on every probe; they ride along at most this often unless a state flips.
	stateRefreshEvery = 5 * time.Minute
	// While a page reads synced devices, pulls follow it this closely; otherwise they idle.
	pullEvery      = 20 * time.Second
	pullIdleEvery  = 5 * time.Minute
	debounceWindow = 2500 * time.Millisecond
	stateFileName  = "cloud-sync.json"
)

// DefaultServer returns the built-in sync service base URL (or LGMON_SYNC_SERVER env override).
func DefaultServer() string {
	if env := strings.TrimSpace(os.Getenv("LGMON_SYNC_SERVER")); env != "" {
		return strings.TrimRight(env, "/")
	}
	return strings.TrimRight(defaultServer, "/")
}

// SecretStore matches internal/secrets.Store for encrypted cloud token persistence.
type SecretStore interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}

// SnapshotProvider supplies this device's live boards, monitors, docs, and profile identity.
type SnapshotProvider interface {
	CloudIdentity(ctx context.Context) (name, avatar string)
	CloudSnapshot(ctx context.Context) (boards []api.BoardInfo, monitors []api.Monitor, docHashes map[string]string, err error)
	CloudMonitorDoc(ctx context.Context, monitorID string) (string, error)
}

// JobSnapshotProvider is optionally implemented alongside SnapshotProvider to sync batch job daily records.
type JobSnapshotProvider interface {
	CloudJobHashes(ctx context.Context) (map[string]string, error)
	CloudJobSnapshot(ctx context.Context, monitorID string) (api.JobDays, bool, error)
}

type SyncStats struct {
	BytesSent      int64 `json:"bytesSent"`
	BytesReceived  int64 `json:"bytesReceived"`
	PushCount      int64 `json:"pushCount"`
	HeartbeatCount int64 `json:"heartbeatCount"`
	PullCount      int64 `json:"pullCount"`
	DocUploads     int64 `json:"docUploads"`
}

type Status struct {
	LoggedIn     bool      `json:"loggedIn"`
	Email        string    `json:"email,omitempty"`
	HasPassword  bool      `json:"hasPassword,omitempty"`
	DeviceID     string    `json:"deviceId"`
	DeviceName   string    `json:"deviceName"`
	ServerURL    string    `json:"serverUrl"`
	Paused       bool      `json:"paused"`
	Syncing      bool      `json:"syncing"`
	LastPushAt   int64     `json:"lastPushAt,omitempty"`
	LastPullAt   int64     `json:"lastPullAt,omitempty"`
	LastError    string    `json:"lastError,omitempty"`
	DevicesCount int       `json:"devicesCount"`
	Stats        SyncStats `json:"stats"`
}

type persistedState struct {
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName,omitempty"`
	ServerURL   string `json:"serverUrl,omitempty"`
	Email       string `json:"email,omitempty"`
	UserID      string `json:"userId,omitempty"`
	HasPassword bool   `json:"hasPassword,omitempty"`
	Paused      bool   `json:"paused,omitempty"`
	LastPushAt  int64  `json:"lastPushAt,omitempty"`
	LastPullAt  int64  `json:"lastPullAt,omitempty"`
}

type Options struct {
	Home     string
	Version  string
	Secrets  SecretStore
	Client   Client
	Now      func() time.Time
	Logger   *slog.Logger
	OnChange func()
}

type cachedDoc struct {
	hash     string
	markdown string
}

type cachedJob struct {
	hash string
	data api.JobDays
}

type inflightJob struct {
	done chan struct{}
	data api.JobDays
	err  error
}

type Manager struct {
	path     string
	version  string
	secrets  SecretStore
	client   Client
	now      func() time.Time
	logger   *slog.Logger
	onChange func()

	mu            sync.RWMutex
	provider      SnapshotProvider
	state         persistedState
	token         string
	syncing       bool
	lastError     string
	stats         SyncStats
	sentMetaHash  string
	sentStateH    string
	sentCritical  string
	lastPushTime  time.Time
	lastStatePush time.Time
	lastPullTime  time.Time
	snapshotSeq   int64
	snapshotETag  string
	rawDevices    map[string]RemoteDevicePayload
	devices       map[string]reconstructedDevice
	deviceOrder   []string
	docCache      map[string]cachedDoc
	jobCache      map[string]cachedJob
	jobInflight   map[string]*inflightJob

	wakeCh chan struct{}
	pushCh chan struct{}
	pullCh chan struct{}
}

func NewManager(opts Options) *Manager {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	client := opts.Client
	if client == nil {
		client = HTTPClient{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		path:        filepath.Join(opts.Home, "data", stateFileName),
		version:     opts.Version,
		secrets:     opts.Secrets,
		client:      client,
		now:         nowFn,
		logger:      logger,
		onChange:    opts.OnChange,
		rawDevices:  map[string]RemoteDevicePayload{},
		devices:     map[string]reconstructedDevice{},
		docCache:    map[string]cachedDoc{},
		jobCache:    map[string]cachedJob{},
		jobInflight: map[string]*inflightJob{},
		wakeCh:      make(chan struct{}, 1),
		pushCh:      make(chan struct{}, 1),
		pullCh:      make(chan struct{}, 1),
	}
	m.load()
	return m
}

func (m *Manager) SetProvider(p SnapshotProvider) {
	m.mu.Lock()
	m.provider = p
	m.mu.Unlock()
}

func (m *Manager) load() {
	var st persistedState
	if raw, err := os.ReadFile(m.path); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	if st.DeviceID == "" {
		buf := make([]byte, 12)
		_, _ = rand.Read(buf)
		st.DeviceID = "dev-" + hex.EncodeToString(buf)
		m.state = st
		_ = m.saveLocked()
	} else {
		m.state = st
	}
	// A token without the account it belongs to is a leftover from signing out and is never used.
	if m.secrets != nil && st.Email != "" {
		if raw, err := m.secrets.Get(context.Background(), sessionSecretKey); err == nil {
			if token := strings.TrimSpace(string(raw)); token != signedOutMarker {
				m.token = token
			}
		}
	}
}

// forgetToken overwrites the stored session; the caller resets the in-memory state.
func (m *Manager) forgetToken(ctx context.Context) {
	if m.secrets == nil {
		return
	}
	if err := m.secrets.Set(ctx, sessionSecretKey, []byte(signedOutMarker)); err != nil {
		m.logger.Warn("Cloud session token was not cleared", "error", err)
	}
}

// resetSyncLocked drops everything learned from the current session. Callers hold m.mu.
func (m *Manager) resetSyncLocked() {
	m.sentMetaHash = ""
	m.sentStateH = ""
	m.sentCritical = ""
	m.lastPushTime = time.Time{}
	m.lastStatePush = time.Time{}
	m.lastPullTime = time.Time{}
	m.snapshotSeq = 0
	m.snapshotETag = ""
	m.rawDevices = map[string]RemoteDevicePayload{}
	m.devices = map[string]reconstructedDevice{}
	m.deviceOrder = nil
	m.docCache = map[string]cachedDoc{}
	m.jobCache = map[string]cachedJob{}
}

// expireSession signs out locally when the service no longer accepts this device's token,
// instead of retrying with it forever.
func (m *Manager) expireSession(ctx context.Context, cause error) {
	m.forgetToken(ctx)
	m.mu.Lock()
	m.token = ""
	m.state.Email = ""
	m.state.UserID = ""
	m.state.HasPassword = false
	m.resetSyncLocked()
	m.lastError = "登录已过期，或本机已在其他设备上解绑，请重新登录"
	_ = m.saveLocked()
	m.mu.Unlock()
	m.logger.Info("Cloud session expired; signed out locally", "cause", cause.Error())
	m.emitChange()
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *Manager) effectiveServerLocked() string {
	if s := strings.TrimSpace(m.state.ServerURL); s != "" {
		return strings.TrimRight(s, "/")
	}
	return DefaultServer()
}

func (m *Manager) effectiveDeviceName(ctx context.Context) (string, string) {
	m.mu.RLock()
	p := m.provider
	custom := strings.TrimSpace(m.state.DeviceName)
	m.mu.RUnlock()
	var profileName, avatar string
	if p != nil {
		profileName, avatar = p.CloudIdentity(ctx)
	}
	if custom != "" {
		return custom, avatar
	}
	if profileName != "" {
		return profileName, avatar
	}
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host), avatar
	}
	return "我的设备", avatar
}

func (m *Manager) signalWake() {
	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
}

// NotifyChange schedules a debounced delta push when local monitors or config change.
func (m *Manager) NotifyChange() {
	m.mu.RLock()
	active := m.token != "" && !m.state.Paused
	m.mu.RUnlock()
	if !active {
		return
	}
	select {
	case m.pushCh <- struct{}{}:
	default:
	}
}

func (m *Manager) emitChange() {
	if m.onChange != nil {
		m.onChange()
	}
}

func (m *Manager) Status(ctx context.Context) Status {
	name, _ := m.effectiveDeviceName(ctx)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Status{
		LoggedIn:     m.token != "" && m.state.Email != "",
		Email:        m.state.Email,
		HasPassword:  m.state.HasPassword,
		DeviceID:     m.state.DeviceID,
		DeviceName:   name,
		ServerURL:    m.effectiveServerLocked(),
		Paused:       m.state.Paused,
		Syncing:      m.syncing,
		LastPushAt:   m.state.LastPushAt,
		LastPullAt:   m.state.LastPullAt,
		LastError:    m.lastError,
		DevicesCount: len(m.deviceOrder),
		Stats:        m.stats,
	}
}

// ---- Authentication & Account Actions ----

type authReply struct {
	Token string `json:"token"`
	User  struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		HasPassword bool   `json:"hasPassword"`
	} `json:"user"`
	Device struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"device"`
}

type OTPSentResult struct {
	OK           bool `json:"ok"`
	CooldownSec  int  `json:"cooldownSec"`
	ExpiresInSec int  `json:"expiresInSec"`
}

// RequestOTP asks for an emailed code. purpose is register (an address not yet signed up), login or reset;
// lang picks the email's language; sign-in mail names this computer so an unexpected request stands out.
func (m *Manager) RequestOTP(ctx context.Context, email, purpose, lang string) (OTPSentResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return OTPSentResult{}, fmt.Errorf("请输入邮箱地址")
	}
	if purpose != "reset" && purpose != "register" {
		purpose = "login"
	}
	if lang != "en" {
		lang = "zh"
	}
	deviceName, _ := m.effectiveDeviceName(ctx)
	m.mu.RLock()
	base := m.effectiveServerLocked()
	m.mu.RUnlock()

	res, err := m.client.Do(ctx, http.MethodPost, base, "/v1/auth/otp/request", "", "", map[string]string{
		"email":      email,
		"purpose":    purpose,
		"lang":       lang,
		"deviceName": deviceName,
		"os":         runtime.GOOS,
	}, false)
	if err != nil {
		return OTPSentResult{}, err
	}
	if res.Status < 200 || res.Status >= 300 {
		return OTPSentResult{}, parseRemoteError(res.Status, res.Body)
	}
	var out OTPSentResult
	_ = json.Unmarshal(res.Body, &out)
	out.OK = true
	return out, nil
}

// Register creates the account with a register code (and an optional password) and binds this device.
func (m *Manager) Register(ctx context.Context, email, code, password, deviceName string) (Status, error) {
	payload := map[string]any{
		"email": strings.TrimSpace(strings.ToLower(email)),
		"code":  strings.TrimSpace(code),
	}
	if password != "" {
		payload["password"] = password
	}
	return m.completeAuth(ctx, "/v1/auth/register", payload, deviceName)
}

func (m *Manager) VerifyOTP(ctx context.Context, email, code, deviceName string) (Status, error) {
	return m.completeAuth(ctx, "/v1/auth/otp/verify", map[string]any{
		"email": strings.TrimSpace(strings.ToLower(email)),
		"code":  strings.TrimSpace(code),
	}, deviceName)
}

func (m *Manager) LoginPassword(ctx context.Context, email, password, deviceName string) (Status, error) {
	return m.completeAuth(ctx, "/v1/auth/login", map[string]any{
		"email":    strings.TrimSpace(strings.ToLower(email)),
		"password": password,
	}, deviceName)
}

func (m *Manager) completeAuth(ctx context.Context, path string, payload map[string]any, deviceName string) (Status, error) {
	defaultName, avatar := m.effectiveDeviceName(ctx)
	name := strings.TrimSpace(deviceName)
	if name == "" {
		name = defaultName
	}
	m.mu.RLock()
	base := m.effectiveServerLocked()
	devID := m.state.DeviceID
	m.mu.RUnlock()

	payload["device"] = DeviceMetaInput{
		ID:      devID,
		Name:    name,
		Avatar:  avatar,
		OS:      runtime.GOOS,
		Version: m.version,
	}

	res, err := m.client.Do(ctx, http.MethodPost, base, path, "", "", payload, false)
	if err != nil {
		return Status{}, err
	}
	if res.Status < 200 || res.Status >= 300 {
		return Status{}, parseRemoteError(res.Status, res.Body)
	}
	var reply authReply
	if err := json.Unmarshal(res.Body, &reply); err != nil || reply.Token == "" {
		return Status{}, fmt.Errorf("云同步服务返回的登录凭证无效")
	}

	if m.secrets != nil {
		if err := m.secrets.Set(ctx, sessionSecretKey, []byte(reply.Token)); err != nil {
			return Status{}, fmt.Errorf("保存登录凭据失败：%w", err)
		}
	}

	m.mu.Lock()
	m.token = reply.Token
	m.state.Email = reply.User.Email
	m.state.UserID = reply.User.ID
	m.state.HasPassword = reply.User.HasPassword
	m.state.DeviceName = name
	m.state.Paused = false
	m.lastError = ""
	m.resetSyncLocked()
	_ = m.saveLocked()
	m.mu.Unlock()

	// Kick off immediate sync in background so login returns fast while pulling devices right away.
	m.signalWake()
	m.emitChange()
	return m.Status(ctx), nil
}

func (m *Manager) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	m.mu.RLock()
	base := m.effectiveServerLocked()
	m.mu.RUnlock()
	res, err := m.client.Do(ctx, http.MethodPost, base, "/v1/auth/password/reset", "", "", map[string]string{
		"email":       strings.TrimSpace(strings.ToLower(email)),
		"code":        strings.TrimSpace(code),
		"newPassword": newPassword,
	}, false)
	if err != nil {
		return err
	}
	if res.Status < 200 || res.Status >= 300 {
		return parseRemoteError(res.Status, res.Body)
	}
	return nil
}

func (m *Manager) SetPassword(ctx context.Context, currentPassword, newPassword string) (Status, error) {
	m.mu.RLock()
	base, token := m.effectiveServerLocked(), m.token
	m.mu.RUnlock()
	if token == "" {
		return Status{}, fmt.Errorf("尚未登录账号")
	}
	res, err := m.client.Do(ctx, http.MethodPost, base, "/v1/auth/password", token, "", map[string]string{
		"currentPassword": currentPassword,
		"newPassword":     newPassword,
	}, false)
	if err != nil {
		return Status{}, err
	}
	if res.Status < 200 || res.Status >= 300 {
		return Status{}, parseRemoteError(res.Status, res.Body)
	}
	m.mu.Lock()
	m.state.HasPassword = true
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emitChange()
	return m.Status(ctx), nil
}

func (m *Manager) Logout(ctx context.Context, unbindDevice bool) (Status, error) {
	m.mu.RLock()
	base, token := m.effectiveServerLocked(), m.token
	m.mu.RUnlock()

	if token != "" {
		_, _ = m.client.Do(ctx, http.MethodPost, base, "/v1/auth/logout", token, "", map[string]bool{
			"unbindDevice": unbindDevice,
		}, false)
	}
	m.forgetToken(ctx)

	m.mu.Lock()
	m.token = ""
	m.state.Email = ""
	m.state.UserID = ""
	m.state.HasPassword = false
	m.state.LastPushAt = 0
	m.state.LastPullAt = 0
	m.lastError = ""
	m.resetSyncLocked()
	_ = m.saveLocked()
	m.mu.Unlock()

	m.emitChange()
	return m.Status(ctx), nil
}

func (m *Manager) SetSettings(ctx context.Context, serverURL *string, paused *bool, deviceName *string) (Status, error) {
	if serverURL != nil {
		trimmed := strings.TrimRight(strings.TrimSpace(*serverURL), "/")
		if trimmed != "" {
			parsed, err := url.Parse(trimmed)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return Status{}, fmt.Errorf("同步服务器地址必须是以 http:// 或 https:// 开头的有效网址")
			}
		}
	}

	m.mu.Lock()
	if serverURL != nil {
		next := strings.TrimRight(strings.TrimSpace(*serverURL), "/")
		effective := next
		if effective == "" {
			effective = DefaultServer()
		}
		// The session token was issued by the current server and means nothing to another one.
		if m.token != "" && effective != m.effectiveServerLocked() {
			m.mu.Unlock()
			return Status{}, fmt.Errorf("请先退出登录，再修改同步服务器地址")
		}
		m.state.ServerURL = next
	}
	if paused != nil {
		m.state.Paused = *paused
	}
	if deviceName != nil {
		m.state.DeviceName = truncateRunes(*deviceName, 40)
		m.sentMetaHash = ""
	}
	_ = m.saveLocked()
	m.mu.Unlock()

	m.signalWake()
	m.emitChange()
	return m.Status(ctx), nil
}

func (m *Manager) RenameDevice(ctx context.Context, deviceID, name string) error {
	name = truncateRunes(name, 40)
	if name == "" {
		return fmt.Errorf("设备名称不能为空")
	}
	m.mu.RLock()
	base, token, selfID := m.effectiveServerLocked(), m.token, m.state.DeviceID
	m.mu.RUnlock()
	if token == "" {
		return fmt.Errorf("尚未登录账号")
	}
	res, err := m.client.Do(ctx, http.MethodPatch, base, "/v1/devices/"+url.PathEscape(deviceID), token, "", map[string]string{
		"name": name,
	}, false)
	if err != nil {
		return err
	}
	if res.Status < 200 || res.Status >= 300 {
		return parseRemoteError(res.Status, res.Body)
	}
	if deviceID == selfID {
		m.mu.Lock()
		m.state.DeviceName = name
		_ = m.saveLocked()
		m.mu.Unlock()
	}
	_ = m.PullOnce(ctx)
	return nil
}

func (m *Manager) UnbindDevice(ctx context.Context, deviceID string) error {
	m.mu.RLock()
	base, token, selfID := m.effectiveServerLocked(), m.token, m.state.DeviceID
	m.mu.RUnlock()
	if token == "" {
		return fmt.Errorf("尚未登录账号")
	}
	if deviceID == selfID {
		_, err := m.Logout(ctx, true)
		return err
	}
	res, err := m.client.Do(ctx, http.MethodDelete, base, "/v1/devices/"+url.PathEscape(deviceID), token, "", nil, false)
	if err != nil {
		return err
	}
	if res.Status < 200 || res.Status >= 300 {
		return parseRemoteError(res.Status, res.Body)
	}
	m.mu.Lock()
	delete(m.rawDevices, deviceID)
	delete(m.devices, deviceID)
	m.snapshotETag = ""
	m.mu.Unlock()
	_ = m.PullOnce(ctx)
	return nil
}

// ---- Core Ultra-Lean Push & Pull Engine ----

func (m *Manager) SyncNow(ctx context.Context) (Status, error) {
	pushErr := m.PushOnce(ctx, true)
	pullErr := m.PullOnce(ctx)
	if pushErr != nil {
		return m.Status(ctx), pushErr
	}
	if pullErr != nil {
		return m.Status(ctx), pullErr
	}
	return m.Status(ctx), nil
}

// failed records a background sync error and signs out when the service rejected the session.
func (m *Manager) failed(ctx context.Context, err error) error {
	if IsUnauthorized(err) {
		m.expireSession(ctx, err)
		return err
	}
	m.recordError(err.Error())
	return err
}

// PushOnce uploads only what changed since the last accepted push, or a keepalive with the two hashes.
// A flipped state, firing count or issue goes out at once; uptime and latency drift waits for
// stateRefreshEvery, because latency differs on nearly every probe.
func (m *Manager) PushOnce(ctx context.Context, force bool) error {
	m.mu.RLock()
	token := m.token
	paused := m.state.Paused
	base := m.effectiveServerLocked()
	provider := m.provider
	prevMeta := m.sentMetaHash
	prevState := m.sentStateH
	prevCritical := m.sentCritical
	lastPush := m.lastPushTime
	lastStatePush := m.lastStatePush
	m.mu.RUnlock()

	if token == "" || paused || provider == nil {
		return nil
	}

	boards, monitors, docHashes, err := provider.CloudSnapshot(ctx)
	if err != nil {
		return err
	}
	var jobHashes map[string]string
	if jp, ok := provider.(JobSnapshotProvider); ok {
		jh, jerr := jp.CloudJobHashes(ctx)
		if jerr != nil {
			m.logger.Warn("Cloud job hash scan failed", "error", jerr)
		} else {
			jobHashes = jh
		}
	}
	nowTime := m.now()
	meta, boardsHash, state, stateHash := BuildCompactSnapshotWithJobs(boards, monitors, docHashes, jobHashes, nowTime.UnixMilli())
	critical := CriticalStateHash(state)
	devName, devAvatar := m.effectiveDeviceName(ctx)
	// The device's name and avatar travel with the meta, so a profile change re-sends it.
	metaHash := HashJSON([]string{boardsHash, devName, devAvatar})

	sendMeta := prevMeta == "" || metaHash != prevMeta
	sendState := prevState == "" || critical != prevCritical ||
		(stateHash != prevState && (force || nowTime.Sub(lastStatePush) >= stateRefreshEvery))
	heartbeatDue := lastPush.IsZero() || nowTime.Sub(lastPush) >= heartbeatEvery
	if !sendMeta && !sendState && !force && !heartbeatDue {
		return nil
	}

	device := &DeviceMetaInput{Name: devName, Avatar: devAvatar, OS: runtime.GOOS, Version: m.version}
	// Unsent parts keep the hash the server already holds, so it does not ask for them again.
	req := SyncPushRequest{MetaHash: prevMeta, StateHash: prevState}
	if sendMeta {
		req.Meta, req.MetaHash, req.Device = &meta, metaHash, device
	}
	if sendState {
		req.State, req.StateHash = &state, stateHash
	}
	heartbeatOnly := req.Meta == nil && req.State == nil

	m.mu.Lock()
	m.syncing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.syncing = false
		m.mu.Unlock()
	}()

	reply, err := m.push(ctx, base, token, req)
	if err != nil {
		return m.failed(ctx, err)
	}
	// The server lost what it had (a fresh database, say): send everything once.
	if (reply.NeedMeta && req.Meta == nil) || (reply.NeedState && req.State == nil) {
		full := SyncPushRequest{Device: device, MetaHash: metaHash, StateHash: stateHash, Meta: &meta, State: &state}
		if reply, err = m.push(ctx, base, token, full); err != nil {
			return m.failed(ctx, err)
		}
		sendMeta, sendState, heartbeatOnly = true, true, false
	}

	if len(reply.NeedDocs) > 0 {
		m.uploadDocs(ctx, base, token, provider, reply.NeedDocs)
	}
	if len(reply.NeedJobs) > 0 {
		if jp, ok := provider.(JobSnapshotProvider); ok {
			m.uploadJobs(ctx, base, token, jp, reply.NeedJobs)
		}
	}

	m.mu.Lock()
	if sendMeta {
		m.sentMetaHash = metaHash
	}
	if sendState {
		m.sentStateH = stateHash
		m.sentCritical = critical
		m.lastStatePush = nowTime
	}
	m.lastPushTime = nowTime
	m.state.LastPushAt = nowTime.UnixMilli()
	m.lastError = ""
	if heartbeatOnly {
		m.stats.HeartbeatCount++
	} else {
		m.stats.PushCount++
	}
	_ = m.saveLocked()
	m.mu.Unlock()
	return nil
}

func (m *Manager) push(ctx context.Context, base, token string, req SyncPushRequest) (SyncPushResponse, error) {
	var reply SyncPushResponse
	res, err := m.client.Do(ctx, http.MethodPost, base, "/v1/sync/push", token, "", req, true)
	if err != nil {
		return reply, err
	}
	m.addTraffic(res.BytesSent, res.BytesReceived)
	if res.Status < 200 || res.Status >= 300 {
		return reply, parseRemoteError(res.Status, res.Body)
	}
	if err := json.Unmarshal(res.Body, &reply); err != nil {
		return reply, fmt.Errorf("解析云同步回执失败：%w", err)
	}
	return reply, nil
}

// uploadDocs sends only the Markdown the server asked for; a failure is retried on a later push,
// because the server keeps asking until the stored hash matches.
func (m *Manager) uploadDocs(ctx context.Context, base, token string, provider SnapshotProvider, ids []string) {
	docs := make([]SyncDocItem, 0, len(ids))
	for _, id := range ids {
		md, err := provider.CloudMonitorDoc(ctx, id)
		if err != nil || strings.TrimSpace(md) == "" {
			continue
		}
		docs = append(docs, SyncDocItem{MonitorID: id, Hash: HashDoc(md), Markdown: md})
	}
	if len(docs) == 0 {
		return
	}
	res, err := m.client.Do(ctx, http.MethodPut, base, "/v1/sync/docs", token, "", SyncDocsRequest{Docs: docs}, true)
	if err != nil {
		m.logger.Warn("Cloud doc upload failed", "error", err)
		return
	}
	m.addTraffic(res.BytesSent, res.BytesReceived)
	if res.Status >= 200 && res.Status < 300 {
		m.mu.Lock()
		m.stats.DocUploads += int64(len(docs))
		m.mu.Unlock()
	}
}

// uploadJobs sends only the batch job daily snapshots the server asked for; failed items are retried on the next push.
func (m *Manager) uploadJobs(ctx context.Context, base, token string, provider JobSnapshotProvider, ids []string) {
	const maxBatchBytes = 560 * 1024
	var batch []SyncJobItem
	batchBytes := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		res, err := m.client.Do(ctx, http.MethodPut, base, "/v1/sync/jobs", token, "", SyncJobsRequest{Jobs: batch}, true)
		if err != nil {
			m.logger.Warn("Cloud batch job upload failed", "error", err)
		} else {
			m.addTraffic(res.BytesSent, res.BytesReceived)
			if res.Status < 200 || res.Status >= 300 {
				m.logger.Warn("Cloud batch job upload rejected", "status", res.Status)
			}
		}
		batch = nil
		batchBytes = 0
	}
	for _, id := range ids {
		snap, ok, err := provider.CloudJobSnapshot(ctx, id)
		if err != nil || !ok {
			continue
		}
		trimmed := TrimJobDaysForSync(snap, MaxJobSnapshotBytes)
		raw, err := json.Marshal(trimmed)
		if err != nil {
			continue
		}
		if len(batch) > 0 && batchBytes+len(raw) > maxBatchBytes {
			flush()
		}
		batch = append(batch, SyncJobItem{MonitorID: id, Hash: HashJSON(trimmed), Payload: trimmed})
		batchBytes += len(raw)
	}
	flush()
}

// PullOnce fetches what changed on the account's devices since the last pull (since=<seq>, ETag/304).
func (m *Manager) PullOnce(ctx context.Context) error {
	return m.pull(ctx, true)
}

func (m *Manager) pull(ctx context.Context, allowResync bool) error {
	m.mu.RLock()
	token := m.token
	paused := m.state.Paused
	base := m.effectiveServerLocked()
	since := m.snapshotSeq
	etag := m.snapshotETag
	m.mu.RUnlock()

	if token == "" || paused {
		return nil
	}

	path := fmt.Sprintf("/v1/sync/snapshot?since=%d", since)
	res, err := m.client.Do(ctx, http.MethodGet, base, path, token, etag, nil, false)
	if err != nil {
		return m.failed(ctx, err)
	}
	m.addTraffic(res.BytesSent, res.BytesReceived)

	nowTime := m.now()
	if res.Status == http.StatusNotModified {
		m.mu.Lock()
		m.lastPullTime = nowTime
		m.state.LastPullAt = nowTime.UnixMilli()
		m.stats.PullCount++
		m.lastError = ""
		m.mu.Unlock()
		return nil
	}
	if res.Status < 200 || res.Status >= 300 {
		return m.failed(ctx, parseRemoteError(res.Status, res.Body))
	}

	var snap SyncSnapshotResponse
	if err := json.Unmarshal(res.Body, &snap); err != nil {
		return fmt.Errorf("解析多设备同步快照失败：%w", err)
	}

	m.mu.Lock()
	missing := false
	seen := make(map[string]bool, len(snap.Devices))
	order := make([]string, 0, len(snap.Devices))
	for _, incoming := range snap.Devices {
		seen[incoming.ID] = true
		order = append(order, incoming.ID)
		merged := incoming
		prev, known := m.rawDevices[incoming.ID]
		if merged.Meta == nil {
			switch {
			case known && prev.Meta != nil && (merged.MetaHash == "" || merged.MetaHash == prev.MetaHash):
				merged.Meta = prev.Meta
			case merged.MetaHash != "":
				missing = true
			}
			// The server sends the avatar only together with the meta it belongs to.
			if known {
				merged.Avatar = prev.Avatar
			}
		}
		if merged.State == nil {
			switch {
			case known && prev.State != nil && (merged.StateHash == "" || merged.StateHash == prev.StateHash):
				merged.State = prev.State
			case merged.StateHash != "":
				missing = true
			}
		}
		m.rawDevices[incoming.ID] = merged
		m.devices[incoming.ID] = ReconstructDevice(merged)
	}
	for oldID := range m.rawDevices {
		if !seen[oldID] {
			delete(m.rawDevices, oldID)
			delete(m.devices, oldID)
		}
	}
	m.deviceOrder = order
	if snap.Seq > m.snapshotSeq {
		m.snapshotSeq = snap.Seq
	}
	if res.ETag != "" {
		m.snapshotETag = res.ETag
	}
	if missing {
		// A delta referred to data this copy never received: start over from a full snapshot.
		m.snapshotSeq, m.snapshotETag = 0, ""
	}
	m.lastPullTime = nowTime
	m.state.LastPullAt = nowTime.UnixMilli()
	m.stats.PullCount++
	m.lastError = ""
	_ = m.saveLocked()
	m.mu.Unlock()

	if missing && allowResync {
		return m.pull(ctx, false)
	}
	m.emitChange()
	return nil
}

// wantFreshPull asks the loop for a pull when a page reads synced devices and the copy is old,
// so pulls follow someone looking instead of running at full rate all day.
func (m *Manager) wantFreshPull() {
	m.mu.RLock()
	stale := m.token != "" && !m.state.Paused && m.now().Sub(m.lastPullTime) >= pullEvery
	m.mu.RUnlock()
	if !stale {
		return
	}
	select {
	case m.pullCh <- struct{}{}:
	default:
	}
}

// Run executes the background sync loop until ctx is canceled.
func (m *Manager) Run(ctx context.Context) {
	heartbeatTicker := time.NewTicker(heartbeatEvery)
	idlePullTicker := time.NewTicker(pullIdleEvery)
	defer heartbeatTicker.Stop()
	defer idlePullTicker.Stop()

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time

	runBoth := func(force bool) {
		if ctx.Err() != nil {
			return
		}
		if err := m.PushOnce(ctx, force); err != nil && IsUnauthorized(err) {
			return
		}
		_ = m.PullOnce(ctx)
	}

	// Lets local collectors finish their first probe before the first push.
	startupTimer := time.NewTimer(2 * time.Second)
	defer startupTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		case <-startupTimer.C:
			runBoth(true)
		case <-m.wakeCh:
			runBoth(true)
		case <-m.pushCh:
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(debounceWindow)
				debounceC = debounceTimer.C
			} else {
				debounceTimer.Reset(debounceWindow)
			}
		case <-debounceC:
			debounceC = nil
			debounceTimer = nil
			_ = m.PushOnce(ctx, false)
		case <-heartbeatTicker.C:
			_ = m.PushOnce(ctx, false)
		case <-m.pullCh:
			_ = m.PullOnce(ctx)
		case <-idlePullTicker.C:
			_ = m.PullOnce(ctx)
		}
	}
}

func (m *Manager) addTraffic(sent, recv int64) {
	m.mu.Lock()
	m.stats.BytesSent += sent
	m.stats.BytesReceived += recv
	m.mu.Unlock()
}

func (m *Manager) recordError(msg string) {
	m.mu.Lock()
	m.lastError = msg
	m.mu.Unlock()
}

// ---- Query methods for local API / frontend ----

func (m *Manager) Devices() []DeviceSummary {
	m.wantFreshPull()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DeviceSummary, 0, len(m.deviceOrder))
	for _, id := range m.deviceOrder {
		if dev, ok := m.devices[id]; ok {
			out = append(out, dev.Summary)
		}
	}
	return out
}

func (m *Manager) DeviceBoard(deviceID, boardID string) (SyncedBoardView, error) {
	m.wantFreshPull()
	m.mu.RLock()
	defer m.mu.RUnlock()
	dev, ok := m.devices[deviceID]
	if !ok {
		return SyncedBoardView{}, fmt.Errorf("未找到设备 %s", deviceID)
	}
	board, ok := dev.Boards[boardID]
	if !ok {
		return SyncedBoardView{}, fmt.Errorf("设备「%s」下不存在面板 %s", dev.Summary.Name, boardID)
	}
	return SyncedBoardView{
		DeviceID:     dev.Summary.ID,
		DeviceName:   dev.Summary.Name,
		DeviceAvatar: dev.Summary.Avatar,
		Online:       dev.Summary.Online,
		LastSeen:     dev.Summary.LastSeen,
		StateUpdated: dev.Summary.StateUpdated,
		Board:        board,
	}, nil
}

func (m *Manager) DeviceMonitor(deviceID, monitorID string) (api.MonitorDetail, error) {
	m.mu.RLock()
	dev, ok := m.devices[deviceID]
	m.mu.RUnlock()
	if !ok {
		return api.MonitorDetail{}, fmt.Errorf("未找到设备 %s", deviceID)
	}
	detail, found := ReconstructMonitorDetail(dev, monitorID, m.now().UnixMilli())
	if !found {
		return api.MonitorDetail{}, fmt.Errorf("设备「%s」下不存在服务 %s", dev.Summary.Name, monitorID)
	}
	return detail, nil
}

// DeviceMonitorDoc fetches a remote device's Markdown doc on demand with ETag + local cache.
func (m *Manager) DeviceMonitorDoc(ctx context.Context, deviceID, monitorID string) (string, error) {
	m.mu.RLock()
	base, token := m.effectiveServerLocked(), m.token
	dev, hasDev := m.devices[deviceID]
	expectedHash := ""
	if hasDev {
		expectedHash = dev.DocHash[monitorID]
	}
	cacheKey := deviceID + "/" + monitorID
	cached, hasCached := m.docCache[cacheKey]
	m.mu.RUnlock()

	if expectedHash == "" {
		return "", nil
	}
	if hasCached && cached.hash == expectedHash {
		return cached.markdown, nil
	}
	if token == "" {
		return "", fmt.Errorf("尚未登录账号")
	}

	ifNoneMatch := ""
	if hasCached && cached.hash != "" {
		ifNoneMatch = `"` + cached.hash + `"`
	}
	path := "/v1/sync/devices/" + url.PathEscape(deviceID) + "/docs/" + url.PathEscape(monitorID)
	res, err := m.client.Do(ctx, http.MethodGet, base, path, token, ifNoneMatch, nil, false)
	if err != nil {
		if hasCached {
			return cached.markdown, nil
		}
		return "", err
	}
	m.addTraffic(res.BytesSent, res.BytesReceived)
	if res.Status == http.StatusNotModified && hasCached {
		return cached.markdown, nil
	}
	if res.Status == http.StatusNotFound {
		return "", nil
	}
	if res.Status < 200 || res.Status >= 300 {
		return "", parseRemoteError(res.Status, res.Body)
	}
	var doc SyncDocResponse
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		return "", fmt.Errorf("解析远程服务文档失败：%w", err)
	}
	m.mu.Lock()
	m.docCache[cacheKey] = cachedDoc{hash: doc.Hash, markdown: doc.Markdown}
	m.mu.Unlock()
	return doc.Markdown, nil
}

// fetchDeviceJob retrieves a remote device's 30-day batch job snapshot on demand with ETag + single-flight cache.
func (m *Manager) fetchDeviceJob(ctx context.Context, deviceID, monitorID string) (api.JobDays, reconstructedDevice, error) {
	m.wantFreshPull()
	m.mu.Lock()
	base, token := m.effectiveServerLocked(), m.token
	dev, hasDev := m.devices[deviceID]
	if !hasDev {
		m.mu.Unlock()
		return api.JobDays{}, reconstructedDevice{}, fmt.Errorf("未找到设备 %s", deviceID)
	}
	mon, hasMon := dev.Monitors[monitorID]
	if !hasMon {
		m.mu.Unlock()
		return api.JobDays{}, reconstructedDevice{}, fmt.Errorf("设备「%s」下不存在服务 %s", dev.Summary.Name, monitorID)
	}
	if mon.Kind != "job" {
		m.mu.Unlock()
		return api.JobDays{}, reconstructedDevice{}, fmt.Errorf("%s 不是批处理任务", monitorID)
	}
	expectedHash := dev.JobHash[monitorID]
	cacheKey := deviceID + "/" + monitorID
	cached, hasCached := m.jobCache[cacheKey]
	if expectedHash != "" && hasCached && cached.hash == expectedHash {
		m.mu.Unlock()
		return cached.data, dev, nil
	}
	if expectedHash == "" {
		m.mu.Unlock()
		return api.JobDays{}, dev, fmt.Errorf("设备「%s」的批处理任务每日记录尚未同步，请确认原设备已升级并完成同步", dev.Summary.Name)
	}
	if token == "" {
		m.mu.Unlock()
		return api.JobDays{}, dev, fmt.Errorf("尚未登录账号")
	}
	// Coalesce concurrent /days and /days/{today} requests from JobSection into one network fetch.
	if inflight, busy := m.jobInflight[cacheKey]; busy {
		m.mu.Unlock()
		select {
		case <-inflight.done:
			return inflight.data, dev, inflight.err
		case <-ctx.Done():
			return api.JobDays{}, dev, ctx.Err()
		}
	}
	inflight := &inflightJob{done: make(chan struct{})}
	m.jobInflight[cacheKey] = inflight
	m.mu.Unlock()

	// keep names the hash the data really belongs to; stale fallbacks pass "" so the next read retries.
	finish := func(data api.JobDays, keep string, err error) (api.JobDays, reconstructedDevice, error) {
		inflight.data, inflight.err = data, err
		m.mu.Lock()
		delete(m.jobInflight, cacheKey)
		if err == nil && keep != "" {
			m.jobCache[cacheKey] = cachedJob{hash: keep, data: data}
		}
		m.mu.Unlock()
		close(inflight.done)
		return data, dev, err
	}

	ifNoneMatch := ""
	if hasCached && cached.hash != "" {
		ifNoneMatch = `"` + cached.hash + `"`
	}
	path := "/v1/sync/devices/" + url.PathEscape(deviceID) + "/jobs/" + url.PathEscape(monitorID)
	res, err := m.client.Do(ctx, http.MethodGet, base, path, token, ifNoneMatch, nil, false)
	if err != nil {
		if hasCached {
			return finish(cached.data, "", nil)
		}
		return finish(api.JobDays{}, "", err)
	}
	m.addTraffic(res.BytesSent, res.BytesReceived)
	if res.Status == http.StatusNotModified && hasCached {
		// The service still holds the old record (the device has not uploaded the new one yet).
		return finish(cached.data, "", nil)
	}
	if res.Status == http.StatusNotFound {
		return finish(api.JobDays{}, "", fmt.Errorf("设备「%s」的批处理任务每日记录尚未上传，请稍后重试", dev.Summary.Name))
	}
	if res.Status < 200 || res.Status >= 300 {
		return finish(api.JobDays{}, "", parseRemoteError(res.Status, res.Body))
	}
	var resp SyncJobResponse
	if err := json.Unmarshal(res.Body, &resp); err != nil {
		return finish(api.JobDays{}, "", fmt.Errorf("解析远程批处理任务记录失败：%w", err))
	}
	keep := resp.Hash
	if keep == "" {
		keep = expectedHash
	}
	return finish(resp.Payload, keep, nil)
}

// DeviceJobDays returns up to n recent daily records for a cloud-synced batch job.
func (m *Manager) DeviceJobDays(ctx context.Context, deviceID, monitorID string, n int) (api.JobDays, error) {
	snap, _, err := m.fetchDeviceJob(ctx, deviceID, monitorID)
	if err != nil {
		return api.JobDays{}, err
	}
	out := api.JobDays{Job: snap.Job, Days: snap.Days}
	if out.Days == nil {
		out.Days = []jobs.Day{}
	}
	if n > 0 && len(out.Days) > n {
		out.Days = out.Days[:n]
	}
	return out, nil
}

// DeviceJobDay returns one business day's detail for a cloud-synced batch job, overlaying live
// production-path monitor states from the device's latest synced snapshot.
func (m *Manager) DeviceJobDay(ctx context.Context, deviceID, monitorID, date string) (api.JobDayDetail, error) {
	snap, dev, err := m.fetchDeviceJob(ctx, deviceID, monitorID)
	if err != nil {
		return api.JobDayDetail{}, err
	}
	day := jobs.Day{Date: date, Status: jobs.Unknown, Summary: "云同步仅保留最近 30 天记录", Attempts: []jobs.Attempt{}}
	for _, item := range snap.Days {
		if item.Date == date {
			day = item
			break
		}
	}
	detail := api.JobDayDetail{Job: snap.Job, Day: day, Production: []api.JobDependency{}}
	if len(detail.Day.Attempts) > 0 {
		for i := range detail.Day.Attempts[0].Stages {
			if detail.Day.Attempts[0].Stages[i].ID == "fetch" {
				stage := detail.Day.Attempts[0].Stages[i]
				detail.Fetch = &stage
				break
			}
		}
	}
	for _, depID := range snap.Job.Production {
		mon, ok := dev.Monitors[depID]
		if !ok {
			continue
		}
		state := mon.Overall
		if state == "" {
			state = mon.State
		}
		detail.Production = append(detail.Production, api.JobDependency{
			ID:    depID,
			Name:  mon.Name,
			State: state,
			Issue: mon.Issue,
		})
	}
	return detail, nil
}

// CurrentDevice is this computer's device ID; a dependency on it is an ordinary local one.
func (m *Manager) CurrentDevice() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.DeviceID
}

// SignedIn reports whether this computer is signed in, so other devices' services can be read.
func (m *Manager) SignedIn() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.token != ""
}

// Touch asks for a fresh pull because a page is showing other devices' data; background readers never call it,
// so pulls keep following people rather than timers.
func (m *Manager) Touch() { m.wantFreshPull() }

// Lookup reads one service of another device as last synced, for a dependency that crosses computers.
func (m *Manager) Lookup(deviceID, monitorID string) (device DeviceSummary, monitor api.Monitor, deviceFound, monitorFound bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dev, ok := m.devices[deviceID]
	if !ok {
		return DeviceSummary{}, api.Monitor{}, false, false
	}
	monitor, monitorFound = dev.Monitors[monitorID]
	return dev.Summary, monitor, true, monitorFound
}

// Services lists every service on the account's other devices, ordered by device and then as each device lists them.
func (m *Manager) Services() []api.ExternalCandidate {
	m.wantFreshPull()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []api.ExternalCandidate{}
	for _, id := range m.deviceOrder {
		dev, ok := m.devices[id]
		if !ok || dev.Summary.IsCurrent || id == m.state.DeviceID {
			continue
		}
		boardNames := map[string]string{}
		for _, board := range dev.Summary.Boards {
			boardNames[board.ID] = board.Name
		}
		ids := make([]string, 0, len(dev.Monitors))
		for monitorID := range dev.Monitors {
			ids = append(ids, monitorID)
		}
		slices.Sort(ids)
		for _, monitorID := range ids {
			monitor := dev.Monitors[monitorID]
			item := api.ExternalCandidate{Device: id, DeviceName: dev.Summary.Name, Online: dev.Summary.Online, ID: monitor.ID, Name: monitor.Name, Role: monitor.Role, Parent: monitor.Parent, State: monitor.State, Watching: monitor.Watching}
			for _, board := range monitor.Boards {
				if name := boardNames[board]; name != "" {
					item.Boards = append(item.Boards, name)
				}
			}
			out = append(out, item)
		}
	}
	return out
}
