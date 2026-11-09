package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

func teamCertificate(data []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("成员公有证书格式无效")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析成员公有证书失败：%w", err)
	}
	return certificate, nil
}

func (m *TeamManager) verifyMember(ctx context.Context, leaf *x509.Certificate) error {
	if leaf == nil || !strings.HasPrefix(leaf.Subject.CommonName, "member-") {
		return store.ErrTeamIdentity
	}
	_, err := m.options.Repository.TeamMember(ctx, leaf.Subject.CommonName, leaf.SerialNumber.String())
	return err
}

func (m *TeamManager) allowRate(key string, interval time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.options.Now()
	if at, ok := m.rates[key]; ok && now.Before(at.Add(interval)) {
		return false
	}
	if len(m.rates) >= 10000 {
		for id, at := range m.rates {
			if !now.Before(at.Add(10 * time.Minute)) {
				delete(m.rates, id)
			}
		}
		if len(m.rates) >= 10000 {
			return false
		}
	}
	m.rates[key] = now
	return true
}

func writeTeam(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeTeamError(w http.ResponseWriter, err error) {
	var fault *api.Fault
	if !errors.As(teamFault(err), &fault) {
		fault = &api.Fault{Status: 503, Code: "team_unavailable", Message: "团队服务暂不可用"}
	}
	if fault.Status == 429 {
		w.Header().Set("Retry-After", "10")
	}
	writeTeam(w, fault.Status, map[string]any{"error": map[string]string{"code": fault.Code, "message": fault.Message}})
}

func (m *TeamManager) memberMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /team/v1/authority", func(w http.ResponseWriter, r *http.Request) {
		writeTeam(w, 200, transport.TeamAuthorityResponse{CAPEM: m.options.Authority.CAPEM()})
	})
	mux.HandleFunc("POST /team/v1/join", m.serveTeamJoin)
	mux.HandleFunc("GET /team/v1/shares", m.serveTeamRead)
	mux.HandleFunc("GET /team/v1/shares/{id}", m.serveTeamRead)
	mux.HandleFunc("GET /team/v1/shares/{id}/panels/{panel}", m.serveTeamRead)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeTeam(w, 404, map[string]any{"error": map[string]string{"code": "team_not_found", "message": "成员端口不提供此接口"}})
	})
	return mux
}

func (m *TeamManager) serveTeamJoin(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !m.allowRate("join:"+host, time.Second) {
		writeTeamError(w, &api.Fault{Status: 429, Code: "team_rate_limited", Message: "加入请求过于频繁，请稍后再试"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		writeTeamError(w, invalidAPI("成员配对请求过大或不可读"))
		return
	}
	var input transport.TeamJoinRequest
	if err := api.DecodeBody(data, &input); err != nil {
		writeTeamError(w, err)
		return
	}
	id, certificate, err := m.options.Authority.SignTeamCSR(input.CSRDER)
	if err != nil {
		writeTeamError(w, invalidAPI("成员证书申请无效"))
		return
	}
	block, err := x509.ParseCertificateRequest(input.CSRDER)
	if err != nil || block.Subject.CommonName != id {
		writeTeamError(w, invalidAPI("成员证书身份不一致"))
		return
	}
	leaf, err := teamCertificate(certificate)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	member, err := m.options.Repository.ConsumeTeamInvite(ctx, transport.TeamInviteHash(input.Code), store.TeamMember{ID: id, Serial: leaf.SerialNumber.String()})
	if err != nil {
		writeTeamError(w, err)
		return
	}
	team, err := m.options.Repository.Team(ctx, member.TeamID)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeTeam(w, 200, transport.TeamJoinResponse{TeamID: team.ID, TeamName: team.Name, OwnerName: team.OwnerName, MemberID: member.ID, CAPEM: m.options.Authority.CAPEM(), CertificatePEM: certificate})
}

func (m *TeamManager) sharedDefinition(ctx context.Context, share store.TeamShare, selectors bool) (api.SharedDashboard, error) {
	var value api.SharedDashboard
	if share.RevokedAt != 0 {
		return value, store.ErrTeamNotFound
	}
	definition, err := dashboardDefinition(m.options.Config.Snapshot(), share.DashboardID)
	if err != nil || definition.HTML != nil || definition.Error != nil {
		return value, &api.Fault{Status: 404, Code: "share_unavailable", Message: "负责人面板已移除或配置暂不可用"}
	}
	team, err := m.options.Repository.Team(ctx, share.TeamID)
	if err != nil {
		return value, err
	}
	refresh, _ := definition.Refresh.Parse()
	if refresh < 10*time.Second {
		refresh = 10 * time.Second
	}
	value = api.SharedDashboard{ID: share.ID, TeamID: team.ID, TeamName: team.Name, OwnerName: team.OwnerName, DashboardID: definition.ID, Title: definition.Title, Revision: share.Revision, Version: definition.Version + "." + fmt.Sprint(share.Revision), Refresh: config.Duration(refresh.String()), DefaultRange: definition.DefaultRange, Panels: []api.SharedPanel{}, Collectors: []api.SharedCollector{}}
	for _, panel := range definition.Panels {
		if len(share.Panels) == 0 || slices.Contains(share.Panels, panel.ID) {
			value.Panels = append(value.Panels, api.SharedPanel{ID: panel.ID, Type: panel.Type, Title: panel.Title, Width: panel.Width})
		}
	}
	if selectors {
		query := `SELECT id,coalesce(hostname,id) AS name FROM collectors ORDER BY id`
		if share.Targets != nil {
			query = `SELECT DISTINCT collector_id,collector_id FROM target_status ORDER BY collector_id`
		}
		result, err := m.options.Queries.QueryShare(ctx, query, share.ShareScope, nil)
		if err != nil {
			return value, &api.Fault{Status: 400, Code: "share_scope_unavailable", Message: "分享的主机范围暂不可读，请联系负责人", Cause: err}
		}
		for _, row := range result.Rows {
			if len(row) == 2 {
				id, _ := row[0].(string)
				name, _ := row[1].(string)
				if id != "" {
					value.Collectors = append(value.Collectors, api.SharedCollector{ID: id, Name: name})
				}
			}
		}
	}
	return value, nil
}

func (m *TeamManager) memberValue(ctx context.Context, r *http.Request, member store.TeamMember) (any, *store.TeamShare, string, error) {
	id, panelID := r.PathValue("id"), r.PathValue("panel")
	if id == "" {
		if len(r.URL.Query()) > 0 {
			return nil, nil, "", invalidAPI("共享面板清单不接受查询参数")
		}
		shares, err := m.options.Repository.TeamShares(ctx, member.TeamID)
		if err != nil {
			return nil, nil, "", err
		}
		values := []api.SharedDashboard{}
		for _, share := range shares {
			if share.RevokedAt != 0 || share.Members != nil && !slices.Contains(share.Members, member.ID) {
				continue
			}
			value, err := m.sharedDefinition(ctx, share, false)
			if err == nil {
				values = append(values, value)
			} else {
				var fault *api.Fault
				if !errors.As(err, &fault) || fault.Status != 404 {
					return nil, nil, "", err
				}
			}
		}
		return values, nil, "", nil
	}
	if !store.ValidTeamID(id) {
		return nil, nil, "", store.ErrTeamNotFound
	}
	share, err := m.options.Repository.MemberShare(ctx, id, member)
	if err != nil {
		return nil, nil, "", err
	}
	definition, err := m.sharedDefinition(ctx, share, panelID != "" || len(r.URL.Query()) == 0)
	if err != nil {
		return nil, nil, "", err
	}
	if panelID == "" {
		if len(r.URL.Query()) > 0 {
			return nil, nil, "", invalidAPI("共享定义不接受查询参数")
		}
		return definition, &share, definition.Version, nil
	}
	if !slices.ContainsFunc(definition.Panels, func(panel api.SharedPanel) bool { return panel.ID == panelID }) {
		return nil, nil, "", store.ErrTeamNotFound
	}
	input, err := dashboardParams(r.URL.Query())
	if err != nil {
		return nil, nil, "", err
	}
	if input.Collector != nil && !slices.ContainsFunc(definition.Collectors, func(c api.SharedCollector) bool { return c.ID == *input.Collector }) {
		return nil, nil, "", &api.Fault{Status: 403, Code: "share_collector_denied", Message: "该主机不在分享范围内"}
	}
	if !m.allowRate(member.ID+":"+share.DashboardID+":"+panelID, 10*time.Second) {
		return nil, nil, "", &api.Fault{Status: 429, Code: "team_rate_limited", Message: "同一张卡片十秒内只能读取一次，请稍后刷新"}
	}
	period, err := config.ResolveQueryParams(input, definition.DefaultRange, m.options.Now)
	if err != nil {
		return nil, nil, "", invalidAPI(err.Error())
	}
	scope := share.ShareScope
	if input.Collector != nil {
		scope.Collectors = []string{*input.Collector}
	}
	original, err := dashboardDefinition(m.options.Config.Snapshot(), share.DashboardID)
	if err != nil {
		return nil, nil, "", store.ErrTeamNotFound
	}
	index := slices.IndexFunc(original.Panels, func(panel api.DashboardPanel) bool { return panel.ID == panelID })
	if index < 0 {
		return nil, nil, "", store.ErrTeamNotFound
	}
	panel := original.Panels[index]
	if panel.Error != nil {
		return nil, nil, "", &api.Fault{Status: 400, Code: "share_query_invalid", Message: "该卡片查询暂不可用，请联系负责人"}
	}
	result, err := m.options.Queries.QueryShare(ctx, panel.Query, scope, period.Bindings())
	if err != nil {
		message := "这张卡片查询暂不可用，请联系负责人检查查询与数据范围"
		if errors.Is(err, store.ErrShareDimension) {
			message = store.ErrShareDimension.Error()
		}
		if errors.Is(err, store.ErrShareResultSize) {
			message = store.ErrShareResultSize.Error()
		}
		return nil, nil, "", &api.Fault{Status: 400, Code: "share_query_denied", Message: message, Cause: err}
	}
	if err := store.ValidatePanelResult(panel.Type, result); err != nil {
		return nil, nil, "", &api.Fault{Status: 422, Code: "share_columns", Message: "该卡片结果不符合显示格式，请联系负责人", Cause: err}
	}
	return api.SharedPanelData{QueryResult: api.QueryResult{QueryResult: result, Params: period}, Version: definition.Version, Revision: share.Revision}, &share, definition.Version, nil
}

func (m *TeamManager) serveTeamRead(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 {
		writeTeamError(w, &api.Fault{Status: 401, Code: "team_certificate_required", Message: "此接口需要已登记的成员客户端证书"})
		return
	}
	leaf := r.TLS.PeerCertificates[0]
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	m.guard.RLock()
	member, err := m.options.Repository.TeamMember(ctx, leaf.Subject.CommonName, leaf.SerialNumber.String())
	if err != nil {
		m.guard.RUnlock()
		writeTeamError(w, err)
		return
	}
	lease := &teamLease{member: member.ID, team: member.TeamID, share: r.PathValue("id"), cancel: cancel}
	m.mu.Lock()
	m.leases[lease] = true
	m.mu.Unlock()
	m.guard.RUnlock()
	defer func() { m.mu.Lock(); delete(m.leases, lease); m.mu.Unlock() }()
	select {
	case m.queries <- struct{}{}:
		defer func() { <-m.queries }()
	default:
		writeTeamError(w, &api.Fault{Status: 429, Code: "team_busy", Message: "团队查询繁忙，请稍后重试"})
		return
	}
	value, share, version, err := m.memberValue(ctx, r, member)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > transport.TeamResponseLimit {
		writeTeamError(w, &api.Fault{Status: 413, Code: "team_result_size", Message: "分享结果过大，请负责人缩小查询"})
		return
	}
	m.guard.RLock()
	defer m.guard.RUnlock()
	if err := ctx.Err(); err != nil {
		writeTeamError(w, err)
		return
	}
	if _, err := m.options.Repository.TeamMember(ctx, member.ID, member.Serial); err != nil {
		writeTeamError(w, err)
		return
	}
	if share != nil {
		current, err := m.options.Repository.MemberShare(ctx, share.ID, member)
		if err != nil || current.Revision != share.Revision {
			writeTeamError(w, store.ErrTeamConflict)
			return
		}
		definition, err := m.sharedDefinition(ctx, current, false)
		if err != nil || definition.Version != version {
			writeTeamError(w, store.ErrTeamConflict)
			return
		}
	} else {
		// A list computed before a revocation must not outlive the authorization guard.
		shares, err := m.options.Repository.TeamShares(ctx, member.TeamID)
		if err != nil {
			writeTeamError(w, err)
			return
		}
		for _, listed := range value.([]api.SharedDashboard) {
			if !slices.ContainsFunc(shares, func(v store.TeamShare) bool {
				return v.ID == listed.ID && v.RevokedAt == 0 && v.Revision == listed.Revision && (v.Members == nil || slices.Contains(v.Members, member.ID))
			}) {
				writeTeamError(w, store.ErrTeamConflict)
				return
			}
		}
	}
	if err := m.options.Repository.VisitTeam(ctx, member.ID, member.Serial); err != nil {
		writeTeamError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
