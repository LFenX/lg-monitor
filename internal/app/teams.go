package app

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

type TeamRepository interface {
	CreateTeam(context.Context, store.Team) (store.Team, error)
	Team(context.Context, string) (store.Team, error)
	Teams(context.Context) ([]store.Team, error)
	AddTeamInvite(context.Context, string, string, string, int64) error
	ConsumeTeamInvite(context.Context, string, store.TeamMember) (store.TeamMember, error)
	TeamMember(context.Context, string, string) (store.TeamMember, error)
	TeamMembers(context.Context, string) ([]store.TeamMember, error)
	RevokeTeamMember(context.Context, string, string) error
	VisitTeam(context.Context, string, string) error
	TeamShare(context.Context, string) (store.TeamShare, error)
	TeamShares(context.Context, string) ([]store.TeamShare, error)
	SaveTeamShare(context.Context, store.TeamShare) (store.TeamShare, error)
	RevokeTeamShare(context.Context, string) error
	MemberShare(context.Context, string, store.TeamMember) (store.TeamShare, error)
	JoinedTeams(context.Context) ([]store.JoinedTeam, error)
	JoinedTeam(context.Context, string) (store.JoinedTeam, error)
	AddJoinedTeam(context.Context, store.JoinedTeam) (store.JoinedTeam, error)
	ChangeJoinedAddress(context.Context, string, string) (store.JoinedTeam, error)
	LeaveJoinedTeam(context.Context, string) error
}
type TeamAuthority interface {
	Fingerprint() string
	CAPEM() []byte
	SignTeamCSR([]byte) (string, []byte, error)
	TeamTLS(context.Context, []string, func(context.Context, *x509.Certificate) error) (*tls.Config, error)
}
type TeamQueries interface {
	QueryShare(context.Context, string, store.ShareScope, map[string]any) (rules.QueryResult, error)
}
type TeamOptions struct {
	Config     *config.Manager
	Repository TeamRepository
	Authority  TeamAuthority
	Queries    TeamQueries
	Keys       transport.KeyStore
	Connector  transport.TeamConnector
	Now        func() time.Time
	Random     io.Reader
}
type teamLease struct {
	member, team, share string
	cancel              context.CancelFunc
}
type TeamManager struct {
	options    TeamOptions
	guard      sync.RWMutex
	mu         sync.Mutex
	leases     map[*teamLease]bool
	rates      map[string]time.Time
	changed    chan struct{}
	address    func(context.Context) (string, error)
	disconnect func(string, string)
	queries    chan struct{}
}

func NewTeamManager(opts TeamOptions) (*TeamManager, error) {
	if opts.Config == nil || opts.Repository == nil || opts.Authority == nil || opts.Queries == nil || opts.Keys == nil || opts.Now == nil {
		return nil, fmt.Errorf("团队功能需要配置、认证库、证书、只读查询、密钥库和时钟")
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	if opts.Connector == nil {
		opts.Connector = transport.NativeTeamConnector{}
	}
	return &TeamManager{options: opts, leases: map[*teamLease]bool{}, rates: map[string]time.Time{}, changed: make(chan struct{}, 1), queries: make(chan struct{}, 16)}, nil
}

func (m *TeamManager) notify() {
	select {
	case m.changed <- struct{}{}:
	default:
	}
}
func (m *TeamManager) cancelAccess(member, team, share string) {
	m.mu.Lock()
	for lease := range m.leases {
		if (member == "" || lease.member == member) && (team == "" || lease.team == team) && (share == "" || lease.share == share) {
			lease.cancel()
		}
	}
	closeConnections := m.disconnect
	m.mu.Unlock()
	if closeConnections != nil {
		closeConnections(member, team)
	}
}
func (m *TeamManager) mutate(ctx context.Context, member, team, share string, action func() error) error {
	// Closing sockets first also unblocks a response already being written under the read guard.
	m.cancelAccess(member, team, share)
	m.guard.Lock()
	defer m.guard.Unlock()
	m.cancelAccess(member, team, share)
	if err := ctx.Err(); err != nil {
		return err
	}
	err := action()
	m.notify()
	return err
}

func teamFault(err error) error {
	if err == nil {
		return nil
	}
	var fault *api.Fault
	if errors.As(err, &fault) {
		return fault
	}
	switch {
	case errors.Is(err, store.ErrTeamInput):
		return &api.Fault{Status: 400, Code: "invalid_team", Message: err.Error(), Cause: err}
	case errors.Is(err, store.ErrTeamNotFound):
		return &api.Fault{Status: 404, Code: "team_not_found", Message: store.ErrTeamNotFound.Error(), Cause: err}
	case errors.Is(err, store.ErrTeamInvite):
		return &api.Fault{Status: 400, Code: "invalid_team_invite", Message: store.ErrTeamInvite.Error(), Cause: err}
	case errors.Is(err, store.ErrTeamIdentity):
		return &api.Fault{Status: 403, Code: "team_identity_revoked", Message: store.ErrTeamIdentity.Error(), Cause: err}
	case errors.Is(err, store.ErrTeamConflict):
		return &api.Fault{Status: 409, Code: "share_changed", Message: store.ErrTeamConflict.Error(), Cause: err}
	case errors.Is(err, transport.ErrFingerprint):
		return &api.Fault{Status: 400, Code: "team_fingerprint", Message: "CA 指纹不一致，已停止加入，请与负责人重新核对", Cause: err}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &api.Fault{Status: 504, Code: "team_timeout", Message: "团队读取已取消或达到时间上限，请稍后重试", Cause: err}
	}
	var remote *transport.TeamRemoteError
	if errors.As(err, &remote) {
		status := remote.Status
		if status < 400 || status > 599 {
			status = 502
		}
		return &api.Fault{Status: status, Code: remote.Code, Message: remote.Message, Cause: err}
	}
	return &api.Fault{Status: 503, Code: "team_unavailable", Message: "负责人暂不可连接、成员身份已失效或团队服务尚未就绪，请检查地址并联系负责人", Cause: err}
}

func (m *TeamManager) Execute(ctx context.Context, operation api.Operation, request api.Request) (any, error) {
	value, err := m.execute(ctx, operation, request)
	return value, teamFault(err)
}

func (m *TeamManager) execute(ctx context.Context, operation api.Operation, r api.Request) (any, error) {
	s := m.options.Repository
	switch operation {
	case api.TeamState:
		owned, err := s.Teams(ctx)
		if err != nil {
			return nil, err
		}
		joined, err := s.JoinedTeams(ctx)
		if err != nil {
			return nil, err
		}
		return api.TeamStateResult{Owned: len(owned), Joined: len(joined), Enabled: len(owned)+len(joined) > 0}, nil
	case api.ListTeams:
		return s.Teams(ctx)
	case api.CreateTeam:
		var input api.TeamCreateInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		id, err := transport.NewTeamID("team", m.options.Random)
		if err != nil {
			return nil, err
		}
		if input.OwnerName == "" {
			input.OwnerName = "负责人"
		}
		value, err := s.CreateTeam(ctx, store.Team{ID: id, Name: input.Name, OwnerName: input.OwnerName})
		m.notify()
		return value, err
	case api.ListTeamMembers:
		return s.TeamMembers(ctx, r.Params["id"])
	case api.InviteTeamMember:
		var input api.TeamInviteInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		if _, err := s.Team(ctx, r.Params["id"]); err != nil {
			return nil, err
		}
		ttl := 24 * time.Hour
		if input.TTL != "" {
			parsed, err := input.TTL.Parse()
			if err != nil || parsed < time.Minute || parsed > 7*24*time.Hour {
				return nil, fmt.Errorf("邀请期限须为 1m～7d：%w", store.ErrTeamInput)
			}
			ttl = parsed
		}
		m.mu.Lock()
		address := m.address
		m.mu.Unlock()
		if address == nil {
			return nil, fmt.Errorf("团队 HTTPS 入口尚未启动")
		}
		endpoint, err := address(ctx)
		if err != nil {
			return nil, err
		}
		invitation, hash, err := transport.NewTeamInvitation(endpoint, m.options.Authority.Fingerprint(), r.Params["id"], m.options.Random)
		if err != nil {
			return nil, err
		}
		expires := m.options.Now().Add(ttl).UnixMilli()
		if err := s.AddTeamInvite(ctx, hash, r.Params["id"], input.Name, expires); err != nil {
			return nil, err
		}
		return api.TeamInviteResult{Invitation: invitation, Address: endpoint, Fingerprint: transport.FingerprintShort(m.options.Authority.Fingerprint()), ExpiresAt: expires}, nil
	case api.RemoveTeamMember:
		err := m.mutate(ctx, r.Params["member"], r.Params["id"], "", func() error { return s.RevokeTeamMember(ctx, r.Params["id"], r.Params["member"]) })
		return api.Confirmation{OK: err == nil}, err
	case api.ListShares:
		for key := range r.Query {
			if key != "team" {
				return nil, invalidAPI("分享清单只接受 team 参数")
			}
		}
		return s.TeamShares(ctx, r.Query.Get("team"))
	case api.CreateShare, api.UpdateShare:
		var input api.ShareInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(r.Body, &fields); err != nil {
			return nil, invalidAPI("分享请求格式无效")
		}
		for _, field := range []string{"panels", "collectors", "targets", "members"} {
			if raw, ok := fields[field]; ok && strings.TrimSpace(string(raw)) == "null" {
				return nil, invalidAPI(field + " 不能为 null，不限制时请省略该字段")
			}
		}
		if err := m.validateShare(input); err != nil {
			return nil, err
		}
		value := store.TeamShare{TeamID: input.TeamID, DashboardID: input.DashboardID, Panels: input.Panels, ShareScope: store.ShareScope{Collectors: input.Collectors, Targets: input.Targets}, Members: input.Members, Revision: input.Revision}
		if operation == api.CreateShare {
			if input.Revision != 0 {
				return nil, invalidAPI("新分享不接受 revision")
			}
			id, err := transport.NewTeamID("share", m.options.Random)
			if err != nil {
				return nil, err
			}
			value.ID = id
			m.guard.Lock()
			defer m.guard.Unlock()
			return s.SaveTeamShare(ctx, value)
		}
		if input.Revision < 1 {
			return nil, fmt.Errorf("更新分享须提交当前 revision：%w", store.ErrTeamConflict)
		}
		value.ID = r.Params["id"]
		var updated store.TeamShare
		err := m.mutate(ctx, "", input.TeamID, value.ID, func() error { var err error; updated, err = s.SaveTeamShare(ctx, value); return err })
		return updated, err
	case api.RevokeShare:
		share, err := s.TeamShare(ctx, r.Params["id"])
		if err != nil {
			return nil, err
		}
		err = m.mutate(ctx, "", share.TeamID, share.ID, func() error { return s.RevokeTeamShare(ctx, share.ID) })
		return api.Confirmation{OK: err == nil}, err
	case api.PreviewTeamJoin, api.JoinTeam:
		var input api.TeamJoinInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		invitation, err := transport.ParseTeamInvitation(input.Invitation)
		if err != nil {
			return nil, invalidAPI(err.Error())
		}
		if operation == api.PreviewTeamJoin {
			_, fingerprint, err := transport.PreviewTeamInvitation(ctx, invitation, m.options.Connector, m.options.Now)
			return api.TeamJoinPreview{Address: invitation.Address, Fingerprint: transport.FingerprintShort(fingerprint), TeamID: invitation.TeamID()}, err
		}
		if input.Fingerprint == "" {
			return nil, invalidAPI("请先通过可信渠道核对 CA 指纹，再确认加入")
		}
		joined, err := s.JoinedTeams(ctx)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(joined, func(v store.JoinedTeam) bool {
			return strings.HasPrefix(v.Fingerprint, strings.ToUpper(invitation.Fingerprint)) && v.TeamID == invitation.TeamID()
		}) {
			return nil, invalidAPI("本机已加入该团队，请使用已有身份或先移除本机加入记录")
		}
		response, keyRef, fingerprint, err := transport.JoinTeam(ctx, invitation, input.Fingerprint, m.options.Connector, m.options.Keys, m.options.Random, m.options.Now)
		if err != nil {
			return nil, err
		}
		id, err := transport.NewTeamID("joined", m.options.Random)
		if err != nil {
			return nil, err
		}
		value, err := s.AddJoinedTeam(ctx, store.JoinedTeam{ID: id, TeamID: response.TeamID, TeamName: response.TeamName, OwnerName: response.OwnerName, MemberID: response.MemberID, Address: invitation.Address, Fingerprint: fingerprint, CA: response.CAPEM, Certificate: response.CertificatePEM, KeyRef: keyRef})
		m.notify()
		return value, err
	case api.ListJoinedTeams:
		return s.JoinedTeams(ctx)
	case api.ChangeTeamAddress:
		var input api.TeamAddressInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		if err := transport.ValidateTeamAddress(input.Address); err != nil {
			return nil, invalidAPI(err.Error())
		}
		return s.ChangeJoinedAddress(ctx, r.Params["id"], strings.TrimSuffix(input.Address, "/"))
	case api.LeaveTeam:
		err := s.LeaveJoinedTeam(ctx, r.Params["id"])
		m.notify()
		return api.Confirmation{OK: err == nil}, err
	case api.ListJoinedShares, api.GetJoinedShare, api.GetJoinedPanel:
		return m.readJoined(ctx, operation, r)
	default:
		return nil, invalidAPI("不支持此团队操作")
	}
}

func (m *TeamManager) validateShare(input api.ShareInput) error {
	definition, err := dashboardDefinition(m.options.Config.Snapshot(), input.DashboardID)
	if err != nil {
		return err
	}
	if definition.HTML != nil || definition.Error != nil {
		return invalidAPI("只能分享当前配置有效的声明式面板，不能分享 HTML 页面")
	}
	for _, id := range input.Panels {
		if !slices.ContainsFunc(definition.Panels, func(p api.DashboardPanel) bool { return p.ID == id }) {
			return invalidAPI("分享包含不存在的卡片，请刷新后选择")
		}
	}
	return nil
}

func (m *TeamManager) readJoined(ctx context.Context, operation api.Operation, r api.Request) (any, error) {
	joined, err := m.options.Repository.JoinedTeam(ctx, r.Params["id"])
	if err != nil {
		return nil, err
	}
	configuration, err := transport.LoadTeamTLS(ctx, m.options.Keys, joined.CA, joined.Certificate, joined.KeyRef, joined.Fingerprint, m.options.Now)
	if err != nil {
		return nil, err
	}
	path := "/team/v1/shares"
	if operation != api.ListJoinedShares {
		if !store.ValidTeamID(r.Params["share"]) {
			return nil, invalidAPI("分享标识无效")
		}
		path += "/" + url.PathEscape(r.Params["share"])
	}
	if operation == api.GetJoinedPanel {
		if !config.ValidID(r.Params["panel"]) {
			return nil, invalidAPI("卡片标识无效")
		}
		if _, err := dashboardParams(r.Query); err != nil {
			return nil, err
		}
		path += "/panels/" + url.PathEscape(r.Params["panel"])
		if len(r.Query) > 0 {
			path += "?" + r.Query.Encode()
		}
		var data api.SharedPanelData
		err = m.options.Connector.Exchange(ctx, joined.Address, path, configuration, nil, &data)
		return data, err
	}
	if len(r.Query) > 0 {
		return nil, invalidAPI("分享定义不接受查询参数")
	}
	if operation == api.GetJoinedShare {
		var data api.SharedDashboard
		err = m.options.Connector.Exchange(ctx, joined.Address, path, configuration, nil, &data)
		return data, err
	}
	data := []api.SharedDashboard{}
	err = m.options.Connector.Exchange(ctx, joined.Address, path, configuration, nil, &data)
	return data, err
}

func (m *TeamManager) AdminRPC(ctx context.Context, input *lgmonv1.TeamAdminRequest) ([]byte, error) {
	request := api.Request{Params: map[string]string{}, Query: url.Values{}, Body: input.BodyJson}
	for key, value := range input.Params {
		switch key {
		case "id", "member", "share", "panel":
			request.Params[key] = value
		case "team", "from", "to", "range", "collector":
			request.Query.Set(key, value)
		default:
			return json.Marshal(map[string]any{"error": map[string]string{"code": "invalid_team", "message": "不支持此团队参数"}})
		}
	}
	value, err := m.Execute(ctx, api.Operation(input.Operation), request)
	if err != nil {
		var fault *api.Fault
		if !errors.As(err, &fault) {
			return nil, err
		}
		return json.Marshal(map[string]any{"error": map[string]any{"code": fault.Code, "message": fault.Message, "status": fault.Status}})
	}
	return json.Marshal(value)
}
