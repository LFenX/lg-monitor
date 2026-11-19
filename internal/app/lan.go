package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

// lanTeamID is the team every LAN viewer joins; it exists only to issue and revoke device
// certificates, while what each device may read is decided per board by grants.
const lanTeamID = "team-lan"

const (
	lanRequestTTL  = 24 * time.Hour
	lanMaxPending  = 50
	lanDiscoverFor = 1500 * time.Millisecond
)

// LANBoards gives the member port read access to boards without exposing the rest of the API.
type LANBoards interface {
	LANBoardList(context.Context) ([]api.BoardInfo, error)
	LANBoardView(context.Context, string) (api.BoardView, error)
	LANMonitorDetail(context.Context, string) (api.MonitorDetail, error)
	LANMonitorDoc(context.Context, string) (api.MonitorDoc, error)
}

// PacketNetwork opens the discovery socket; tests use loopback ports instead of the real one.
type PacketNetwork interface {
	ListenPacket(context.Context, string) (net.PacketConn, error)
}

type NativePacketNetwork struct{}

func (NativePacketNetwork) ListenPacket(ctx context.Context, address string) (net.PacketConn, error) {
	var config net.ListenConfig
	return config.ListenPacket(ctx, "udp4", address)
}

type lanRepository interface {
	AddLANRequest(context.Context, store.LANRequest, int, int64) (store.LANRequest, error)
	LANRequest(context.Context, string) (store.LANRequest, error)
	LANRequests(context.Context, int64, int64) ([]store.LANRequest, error)
	DecideLANRequest(context.Context, string, string, string, []byte) error
	AddTeamMember(context.Context, store.TeamMember) (store.TeamMember, error)
	AddLANGrant(context.Context, store.LANGrant) (store.LANGrant, error)
	LANGrants(context.Context) ([]store.LANGrant, error)
	LANGranted(context.Context, string, string) (bool, error)
	RevokeLANGrant(context.Context, string) (store.LANGrant, error)
	AddLANOutgoing(context.Context, store.LANOutgoing) (store.LANOutgoing, error)
	LANOutgoingItem(context.Context, string) (store.LANOutgoing, error)
	LANOutgoing(context.Context) ([]store.LANOutgoing, error)
	UpdateLANOutgoing(context.Context, string, string, string, string) error
	UpdateLANOutgoingOwner(context.Context, string, string, string) error
	DeleteLANOutgoing(context.Context, string) error
}

type lanState struct {
	mu     sync.Mutex
	boards LANBoards
	team   bool
	// remote keeps the last copy of each remote board: the main window and the small window both
	// refresh it, and when the owner turns away a second read within the same second, the copy
	// read a moment ago answers instead. A refusal is never answered from it.
	remote map[string]remoteCopy
}

type remoteCopy struct {
	at    time.Time
	value api.LANRemoteBoard
}

const remoteFresh = 10 * time.Second

// lanBoardReply is a shared board plus who shares it; older viewers read the board and ignore the rest.
type lanBoardReply struct {
	api.BoardView
	Owner *transport.LANOwner `json:"lanOwner,omitempty"`
}

func (m *TeamManager) SetLANBoards(boards LANBoards) {
	m.lan.mu.Lock()
	m.lan.boards = boards
	m.lan.mu.Unlock()
}

func (m *TeamManager) lanBoards() (LANBoards, error) {
	m.lan.mu.Lock()
	defer m.lan.mu.Unlock()
	if m.lan.boards == nil {
		return nil, &api.Fault{Status: 503, Code: "lan_unavailable", Message: "面板还没有准备好，请稍后再试"}
	}
	return m.lan.boards, nil
}

func (m *TeamManager) lanRepository() (lanRepository, error) {
	repository, ok := m.options.Repository.(lanRepository)
	if !ok {
		return nil, &api.Fault{Status: 503, Code: "lan_unavailable", Message: "内网共享需要新版认证库"}
	}
	return repository, nil
}

func (m *TeamManager) hostname() string {
	if m.options.Hostname != "" {
		return m.options.Hostname
	}
	name, _ := os.Hostname()
	return name
}

// EnsureLANTeam creates the team that LAN viewers join, so the member port can start.
func (m *TeamManager) EnsureLANTeam(ctx context.Context) error {
	m.lan.mu.Lock()
	ready := m.lan.team
	m.lan.mu.Unlock()
	if ready {
		return nil
	}
	if _, err := m.options.Repository.Team(ctx, lanTeamID); err != nil {
		if !errors.Is(err, store.ErrTeamNotFound) {
			return err
		}
		if _, err := m.options.Repository.CreateTeam(ctx, store.Team{ID: lanTeamID, Name: "内网", OwnerName: m.hostname()}); err != nil {
			return err
		}
		m.notify()
	}
	m.lan.mu.Lock()
	m.lan.team = true
	m.lan.mu.Unlock()
	return nil
}

func (m *TeamManager) lanVisible() bool {
	return m.options.Config.Snapshot().Settings.LAN.Visible
}

func secretHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func randomHex(random io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("生成随机数失败：%w", err)
	}
	return hex.EncodeToString(value), nil
}

// ---------- the member port: what other computers may ask ----------

func (m *TeamManager) serveLANDirectory(w http.ResponseWriter, r *http.Request) {
	if !m.lanVisible() {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_hidden", Message: "对方没有打开内网可见"})
		return
	}
	boards, err := m.lanBoards()
	if err != nil {
		writeTeamError(w, err)
		return
	}
	list, err := boards.LANBoardList(r.Context())
	if err != nil {
		writeTeamError(w, &api.Fault{Status: 503, Code: "lan_unavailable", Message: "对方的面板暂时读不到"})
		return
	}
	directory := transport.LANDirectory{Name: m.lanName(), Avatar: m.lanAvatar(), Fingerprint: m.options.Authority.Fingerprint(), Boards: []transport.LANBoardCard{}}
	for _, board := range list {
		directory.Boards = append(directory.Boards, transport.LANBoardCard{ID: board.ID, Name: board.Name, Description: board.Description, Services: board.Summary.Services, Children: board.Summary.Children})
	}
	writeTeam(w, 200, directory)
}

// lanMember returns the LAN member presenting a verified client certificate, if any.
func (m *TeamManager) lanMember(r *http.Request) (store.TeamMember, bool, error) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return store.TeamMember{}, false, nil
	}
	leaf := r.TLS.PeerCertificates[0]
	member, err := m.options.Repository.TeamMember(r.Context(), leaf.Subject.CommonName, leaf.SerialNumber.String())
	if err != nil {
		return member, false, err
	}
	if member.TeamID != lanTeamID {
		return member, false, store.ErrTeamIdentity
	}
	return member, true, nil
}

func (m *TeamManager) serveLANRequest(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !m.lanVisible() {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_hidden", Message: "对方没有打开内网可见"})
		return
	}
	// Five a minute per address is plenty for a person and useless for flooding the list.
	if !m.allowRate("lan-request:"+host, 12*time.Second) {
		writeTeamError(w, &api.Fault{Status: 429, Code: "lan_rate_limited", Message: "申请太频繁了，请稍后再试"})
		return
	}
	repository, err := m.lanRepository()
	if err != nil {
		writeTeamError(w, err)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		writeTeamError(w, invalidAPI("申请内容过大或不可读"))
		return
	}
	var input transport.LANRequestInput
	if err := api.DecodeBody(data, &input); err != nil {
		writeTeamError(w, err)
		return
	}
	input.DeviceName, input.Hostname = strings.TrimSpace(input.DeviceName), strings.TrimSpace(input.Hostname)
	if input.DeviceName == "" || utf8.RuneCountInString(input.DeviceName) > 40 || len(input.Hostname) > 255 || len(input.Secret) < 32 || len(input.Secret) > 128 {
		writeTeamError(w, invalidAPI("申请缺少设备名或密钥格式无效"))
		return
	}
	boards, err := m.lanBoards()
	if err != nil {
		writeTeamError(w, err)
		return
	}
	list, err := boards.LANBoardList(r.Context())
	if err != nil {
		writeTeamError(w, err)
		return
	}
	index := slices.IndexFunc(list, func(board api.BoardInfo) bool { return board.ID == input.BoardID })
	if index < 0 {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_board_missing", Message: "对方没有这个面板了"})
		return
	}
	request := store.LANRequest{BoardID: input.BoardID, DeviceName: input.DeviceName, Avatar: remoteAvatar(input.Avatar), Hostname: input.Hostname, Address: host, SecretHash: secretHash(input.Secret)}
	var public []byte
	member, known, err := m.lanMember(r)
	switch {
	case err != nil:
		writeTeamError(w, store.ErrTeamIdentity)
		return
	case known:
		// A device that was approved before asks for another board with the identity it already has.
		request.MemberID = member.ID
		public, err = transportPublicKey(r)
	default:
		if len(input.CSRDER) == 0 || len(input.CSRDER) > 16*1024 {
			writeTeamError(w, invalidAPI("第一次申请需要附带设备证书申请"))
			return
		}
		request.CSR = input.CSRDER
		_, public, err = transport.RequestPublicKey(input.CSRDER)
	}
	if err != nil {
		writeTeamError(w, invalidAPI("设备公钥无效"))
		return
	}
	if request.ID, err = transport.NewTeamID("lanreq", m.options.Random); err != nil {
		writeTeamError(w, err)
		return
	}
	request.Code = transport.LANCode(m.options.Authority.Fingerprint(), public, request.ID)
	if _, err := repository.AddLANRequest(r.Context(), request, lanMaxPending, m.options.Now().Add(-lanRequestTTL).UnixMilli()); err != nil {
		writeTeamError(w, err)
		return
	}
	m.notify()
	writeTeam(w, 200, transport.LANRequestReply{ID: request.ID, Code: request.Code, Status: store.LANPending, OwnerName: m.lanName(), OwnerAvatar: m.lanAvatar(), BoardName: list[index].Name})
}

func transportPublicKey(r *http.Request) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(r.TLS.PeerCertificates[0].PublicKey)
}

func (m *TeamManager) serveLANStatus(w http.ResponseWriter, r *http.Request) {
	if !m.allowRate("lan-status:"+r.PathValue("id"), 500*time.Millisecond) {
		writeTeamError(w, &api.Fault{Status: 429, Code: "lan_rate_limited", Message: "查询太频繁了，请稍后再试"})
		return
	}
	repository, err := m.lanRepository()
	if err != nil {
		writeTeamError(w, err)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		writeTeamError(w, invalidAPI("请求不可读"))
		return
	}
	var input transport.LANStatusInput
	if err := api.DecodeBody(data, &input); err != nil {
		writeTeamError(w, err)
		return
	}
	request, err := repository.LANRequest(r.Context(), r.PathValue("id"))
	if err != nil || subtle.ConstantTimeCompare(request.SecretHash, secretHash(input.Secret)) != 1 {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_request_missing", Message: "对方没有这条申请"})
		return
	}
	if request.Status == store.LANPending && request.CreatedAt < m.options.Now().Add(-lanRequestTTL).UnixMilli() {
		request.Status = store.LANExpired
	}
	reply := transport.LANStatusReply{Status: request.Status}
	if request.Status == store.LANApproved {
		team, _ := m.options.Repository.Team(r.Context(), lanTeamID)
		reply.MemberID, reply.TeamID, reply.TeamName, reply.OwnerName, reply.OwnerAvatar = request.MemberID, lanTeamID, team.Name, m.lanName(), m.lanAvatar()
		reply.CAPEM, reply.CertificatePEM = m.options.Authority.CAPEM(), request.Certificate
	}
	writeTeam(w, 200, reply)
}

// sharedBoard answers for a member granted the board in the path: the board as the owner sees it,
// or false after writing why not. rate names what is being read, so a board and each of its
// services have their own pace.
func (m *TeamManager) sharedBoard(w http.ResponseWriter, r *http.Request, rate string, every time.Duration) (api.BoardView, LANBoards, bool) {
	member, known, err := m.lanMember(r)
	if err != nil || !known {
		writeTeamError(w, &api.Fault{Status: 403, Code: "lan_forbidden", Message: "这台电脑还没有被允许查看"})
		return api.BoardView{}, nil, false
	}
	repository, err := m.lanRepository()
	if err != nil {
		writeTeamError(w, err)
		return api.BoardView{}, nil, false
	}
	id := r.PathValue("id")
	granted, err := repository.LANGranted(r.Context(), member.ID, id)
	if err != nil || !granted {
		writeTeamError(w, &api.Fault{Status: 403, Code: "lan_forbidden", Message: "对方已撤销授权，或没有允许你查看这个面板"})
		return api.BoardView{}, nil, false
	}
	if !m.allowRate(rate+":"+member.ID+":"+id+":"+r.PathValue("monitor"), every) {
		writeTeamError(w, &api.Fault{Status: 429, Code: "lan_rate_limited", Message: "刷新太频繁了，请稍后再试"})
		return api.BoardView{}, nil, false
	}
	boards, err := m.lanBoards()
	if err != nil {
		writeTeamError(w, err)
		return api.BoardView{}, nil, false
	}
	view, err := boards.LANBoardView(r.Context(), id)
	if err != nil {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_board_missing", Message: "对方没有这个面板了"})
		return api.BoardView{}, nil, false
	}
	_ = m.options.Repository.VisitTeam(r.Context(), member.ID, r.TLS.PeerCertificates[0].SerialNumber.String())
	return view, boards, true
}

func (m *TeamManager) serveLANBoard(w http.ResponseWriter, r *http.Request) {
	view, _, ok := m.sharedBoard(w, r, "lan-board", time.Second)
	if !ok {
		return
	}
	writeTeam(w, 200, lanBoardReply{BoardView: sanitizeBoard(view), Owner: &transport.LANOwner{Name: m.lanName(), Avatar: m.lanAvatar()}})
}

// serveLANMonitor and serveLANDoc show a viewer one service of a shared board the way its owner
// sees it, without the means to change it; services outside the board stay out of reach.
func (m *TeamManager) serveLANMonitor(w http.ResponseWriter, r *http.Request) {
	view, boards, ok := m.sharedBoard(w, r, "lan-monitor", 500*time.Millisecond)
	if !ok {
		return
	}
	id := r.PathValue("monitor")
	shared := sharedIDs(view)
	if !shared[id] {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_monitor_missing", Message: "对方的这个面板里没有这个服务"})
		return
	}
	detail, err := boards.LANMonitorDetail(r.Context(), id)
	if err != nil {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_monitor_missing", Message: "对方暂时读不到这个服务"})
		return
	}
	detail.Monitor = sanitizeMonitor(detail.Monitor, shared)
	for index := range detail.Children {
		detail.Children[index] = sanitizeMonitor(detail.Children[index], shared)
	}
	// Only the shared board is named; the owner's other boards are none of the viewer's business.
	detail.Boards = []api.BoardRef{{ID: view.ID, Name: view.Name}}
	writeTeam(w, 200, detail)
}

func (m *TeamManager) serveLANDoc(w http.ResponseWriter, r *http.Request) {
	view, boards, ok := m.sharedBoard(w, r, "lan-doc", 500*time.Millisecond)
	if !ok {
		return
	}
	id := r.PathValue("monitor")
	if !sharedIDs(view)[id] {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_monitor_missing", Message: "对方的这个面板里没有这个服务"})
		return
	}
	doc, err := boards.LANMonitorDoc(r.Context(), id)
	if err != nil {
		writeTeamError(w, &api.Fault{Status: 404, Code: "lan_monitor_missing", Message: "对方暂时读不到这篇文档"})
		return
	}
	writeTeam(w, 200, doc)
}

func sharedIDs(view api.BoardView) map[string]bool {
	shared := map[string]bool{}
	for _, item := range view.Items {
		shared[item.ID] = true
	}
	return shared
}

// isLoopbackHost reports an address that only means something on the computer it is typed into.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sanitizeBoard keeps everything a viewer can read and drops what only works on the owner's computer:
// links into the owner's own pages, addresses on the owner's loopback, and links to unshared services.
func sanitizeBoard(view api.BoardView) api.BoardView {
	view.URL, view.Unassigned = "", []string{}
	shared := sharedIDs(view)
	items := make([]api.Monitor, len(view.Items))
	for index, item := range view.Items {
		items[index] = sanitizeMonitor(item, shared)
	}
	view.Items = items
	return view
}

func sanitizeMonitor(item api.Monitor, shared map[string]bool) api.Monitor {
	item.URL, item.MonitorURL, item.Dashboard = "", "", ""
	// The viewer draws the relations between shared services; a link to a service outside the
	// board would name something the owner did not share.
	inside := func(list []api.MonitorDependency) []api.MonitorDependency {
		return slices.DeleteFunc(slices.Clone(list), func(link api.MonitorDependency) bool { return !shared[link.ID] })
	}
	item.Depends, item.Dependents = inside(item.Depends), inside(item.Dependents)
	// An address on the owner's own computer opens nothing for the viewer; a LAN or public address does,
	// whether the owner set it or it is the address the service is watched at.
	if address, err := url.Parse(item.Entry); err != nil || isLoopbackHost(address.Hostname()) {
		item.Entry, item.EntryGuessed = "", false
	}
	return item
}

// ---------- the local API: this computer's own view of LAN sharing ----------

func (m *TeamManager) lanState(ctx context.Context, listening bool, problem string, addresses []string) (api.LANState, error) {
	settings := m.options.Config.Snapshot().Settings
	state := api.LANState{Visible: settings.LAN.Visible, Name: m.lanName(), Avatar: m.lanAvatar(), Hostname: m.hostname(), Fingerprint: transport.FingerprintShort(m.options.Authority.Fingerprint()), Addresses: addresses, Listening: listening, Problem: problem}
	repository, err := m.lanRepository()
	if err != nil {
		return state, nil
	}
	requests, err := repository.LANRequests(ctx, m.options.Now().Add(-lanRequestTTL).UnixMilli(), m.options.Now().Add(-7*24*time.Hour).UnixMilli())
	if err != nil {
		return state, err
	}
	for _, request := range requests {
		if request.Status == store.LANPending {
			state.Pending++
		}
	}
	grants, err := repository.LANGrants(ctx)
	if err != nil {
		return state, err
	}
	state.Viewers = len(grants)
	return state, nil
}

// normalizeLANAddress accepts what a person types: an IP, host:port, or a full https address.
func normalizeLANAddress(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "/"))
	if value == "" {
		return "", invalidAPI("请填写对方地址，例如 192.168.1.20")
	}
	if !strings.Contains(value, "://") {
		if _, _, err := net.SplitHostPort(value); err != nil {
			value = net.JoinHostPort(strings.Trim(value, "[]"), "7445")
		}
		value = "https://" + value
	}
	if err := transport.ValidateTeamAddress(value); err != nil {
		return "", invalidAPI("对方地址格式不对，例如 192.168.1.20 或 https://192.168.1.20:7445")
	}
	return value, nil
}

func (m *TeamManager) directory(ctx context.Context, address string) (transport.LANDirectory, []byte, string, error) {
	var directory transport.LANDirectory
	caPEM, fingerprint, err := transport.FetchTeamAuthority(ctx, address, m.options.Connector, m.options.Now)
	if err != nil {
		return directory, nil, "", &api.Fault{Status: 502, Code: "lan_unreachable", Message: "连不上对方：对方不在线、没有打开内网可见，或防火墙拦住了 7445 端口", Cause: err}
	}
	pinned, err := transport.PinnedTLS(caPEM, m.options.Now)
	if err != nil {
		return directory, nil, "", err
	}
	if err := m.options.Connector.Exchange(ctx, address, "/team/v1/directory", pinned, nil, &directory); err != nil {
		return directory, nil, "", lanRemoteFault(err)
	}
	if directory.Fingerprint != fingerprint {
		return directory, nil, "", &api.Fault{Status: 502, Code: "lan_mismatch", Message: "对方的证书前后不一致，已停止连接"}
	}
	return directory, caPEM, fingerprint, nil
}

func lanRemoteFault(err error) error {
	var remote *transport.TeamRemoteError
	if errors.As(err, &remote) {
		return &api.Fault{Status: 502, Code: remote.Code, Message: "对方回复：" + remote.Message}
	}
	return &api.Fault{Status: 502, Code: "lan_unreachable", Message: "连不上对方，请确认对方在线并打开了内网可见", Cause: err}
}

func (m *TeamManager) discover(ctx context.Context) ([]api.LANPeerResult, error) {
	packets := m.options.Packets
	if packets == nil {
		packets = NativePacketNetwork{}
	}
	conn, err := packets.ListenPacket(ctx, "0.0.0.0:0")
	if err != nil {
		return nil, &api.Fault{Status: 503, Code: "lan_discovery_failed", Message: "无法发送内网发现请求", Cause: err}
	}
	defer conn.Close()
	port := m.options.DiscoveryPort
	if port == 0 {
		port = transport.LANDiscoveryPort
	}
	targets := transport.LANTargets
	if m.options.DiscoveryTargets != nil {
		targets = m.options.DiscoveryTargets
	}
	peers, err := transport.DiscoverLAN(conn, targets(port), time.Now().Add(lanDiscoverFor))
	if err != nil {
		return nil, &api.Fault{Status: 503, Code: "lan_discovery_failed", Message: err.Error()}
	}
	own := m.options.Authority.Fingerprint()
	result := make([]api.LANPeerResult, 0, len(peers))
	for _, peer := range peers {
		if strings.HasPrefix(own, peer.Fingerprint) {
			continue
		}
		result = append(result, api.LANPeerResult{LANPeer: peer})
	}
	var wait sync.WaitGroup
	for index := range result {
		wait.Add(1)
		go func() {
			defer wait.Done()
			lookup, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			directory, _, fingerprint, err := m.directory(lookup, result[index].Address)
			switch {
			case err != nil:
				result[index].Problem = err.Error()
			case !strings.HasPrefix(fingerprint, result[index].Fingerprint):
				result[index].Problem = "对方回应的指纹和发现时不一致，已忽略"
			default:
				result[index].Directory = &directory
			}
		}()
	}
	wait.Wait()
	return result, nil
}

func (m *TeamManager) lanJoined(ctx context.Context, fingerprint string) (store.JoinedTeam, bool, error) {
	joined, err := m.options.Repository.JoinedTeams(ctx)
	if err != nil {
		return store.JoinedTeam{}, false, err
	}
	for _, item := range joined {
		if item.TeamID == lanTeamID && item.Fingerprint == fingerprint {
			value, err := m.options.Repository.JoinedTeam(ctx, item.ID)
			return value, err == nil, err
		}
	}
	return store.JoinedTeam{}, false, nil
}

func (m *TeamManager) requestLAN(ctx context.Context, input api.LANRequestCreate) (store.LANOutgoing, error) {
	repository, err := m.lanRepository()
	if err != nil {
		return store.LANOutgoing{}, err
	}
	address, err := normalizeLANAddress(input.Address)
	if err != nil {
		return store.LANOutgoing{}, err
	}
	directory, caPEM, fingerprint, err := m.directory(ctx, address)
	if err != nil {
		return store.LANOutgoing{}, err
	}
	if input.Fingerprint != "" {
		if err := transport.VerifyFingerprint(fingerprint, input.Fingerprint); err != nil {
			return store.LANOutgoing{}, &api.Fault{Status: 409, Code: "lan_mismatch", Message: "对方的指纹和发现时看到的不一样，已停止申请"}
		}
	}
	if fingerprint == m.options.Authority.Fingerprint() {
		return store.LANOutgoing{}, invalidAPI("这是你自己的电脑，不用申请")
	}
	board := slices.IndexFunc(directory.Boards, func(item transport.LANBoardCard) bool { return item.ID == input.BoardID })
	if board < 0 {
		return store.LANOutgoing{}, &api.Fault{Status: 404, Code: "lan_board_missing", Message: "对方没有这个面板"}
	}
	secret, err := randomHex(m.options.Random, 32)
	if err != nil {
		return store.LANOutgoing{}, err
	}
	outgoing := store.LANOutgoing{Address: address, OwnerName: directory.Name, OwnerAvatar: remoteAvatar(directory.Avatar), Fingerprint: fingerprint, BoardID: input.BoardID, BoardName: directory.Boards[board].Name, CA: caPEM, Secret: secret}
	// The nickname and avatar say who is asking; the computer's name stays on this computer.
	request := transport.LANRequestInput{BoardID: input.BoardID, DeviceName: m.lanName(), Avatar: m.lanAvatar(), Secret: secret}
	configuration, err := transport.PinnedTLS(caPEM, m.options.Now)
	if err != nil {
		return outgoing, err
	}
	var public []byte
	joined, known, err := m.lanJoined(ctx, fingerprint)
	if err != nil {
		return outgoing, err
	}
	if known {
		// The existing identity is reused, so one device stays one member however many boards it views.
		configuration, err = transport.LoadTeamTLS(ctx, m.options.Keys, joined.CA, joined.Certificate, joined.KeyRef, fingerprint, m.options.Now)
		if err != nil {
			return outgoing, err
		}
		if public, err = transport.MemberPublicKey(joined.Certificate); err != nil {
			return outgoing, err
		}
		outgoing.JoinedID = joined.ID
	} else {
		_, key, csr, publicKey, err := transport.NewLANIdentity(m.options.Random)
		if err != nil {
			return outgoing, err
		}
		if outgoing.KeyRef, err = transport.SaveTeamKey(ctx, m.options.Keys, m.options.Random, key); err != nil {
			return outgoing, err
		}
		request.CSRDER, public = csr, publicKey
	}
	var reply transport.LANRequestReply
	err = m.options.Connector.Exchange(ctx, address, "/team/v1/requests", configuration, request, &reply)
	var remote *transport.TeamRemoteError
	if errors.As(err, &remote) && remote.Code == "invalid_request" && request.Avatar != "" {
		// Before 0.14 an owner rejects fields it does not know; it still takes the request without the avatar.
		request.Avatar = ""
		err = m.options.Connector.Exchange(ctx, address, "/team/v1/requests", configuration, request, &reply)
	}
	if err != nil {
		return outgoing, lanRemoteFault(err)
	}
	if avatar := remoteAvatar(reply.OwnerAvatar); avatar != "" {
		outgoing.OwnerAvatar = avatar
	}
	if code := transport.LANCode(fingerprint, public, reply.ID); code != reply.Code {
		return outgoing, &api.Fault{Status: 502, Code: "lan_mismatch", Message: "对方给出的配对码对不上，已停止申请"}
	}
	outgoing.RemoteID, outgoing.Code, outgoing.Status = reply.ID, reply.Code, reply.Status
	if outgoing.ID, err = transport.NewTeamID("lanout", m.options.Random); err != nil {
		return outgoing, err
	}
	return repository.AddLANOutgoing(ctx, outgoing)
}

// refreshOutgoing asks the owner whether a pending request was decided, and on approval keeps
// the issued certificate as this device's identity towards that owner.
func (m *TeamManager) refreshOutgoing(ctx context.Context, repository lanRepository, item store.LANOutgoing) store.LANOutgoing {
	if item.Status != store.LANPending {
		return item
	}
	configuration, err := transport.PinnedTLS(item.CA, m.options.Now)
	if err != nil {
		return item
	}
	var reply transport.LANStatusReply
	if err := m.options.Connector.Exchange(ctx, item.Address, "/team/v1/requests/"+item.RemoteID+"/status", configuration, transport.LANStatusInput{Secret: item.Secret}, &reply); err != nil {
		return item
	}
	switch reply.Status {
	case store.LANApproved:
		joined := item.JoinedID
		if joined == "" {
			if item.KeyRef == "" || reply.TeamID != lanTeamID || len(reply.CertificatePEM) == 0 {
				return item
			}
			if _, err := transport.LoadTeamTLS(ctx, m.options.Keys, item.CA, reply.CertificatePEM, item.KeyRef, item.Fingerprint, m.options.Now); err != nil {
				return item
			}
			id, err := transport.NewTeamID("joined", m.options.Random)
			if err != nil {
				return item
			}
			value, err := m.options.Repository.AddJoinedTeam(ctx, store.JoinedTeam{ID: id, TeamID: lanTeamID, TeamName: reply.TeamName, OwnerName: reply.OwnerName, MemberID: reply.MemberID, Address: item.Address, Fingerprint: item.Fingerprint, CA: item.CA, Certificate: reply.CertificatePEM, KeyRef: item.KeyRef})
			if err != nil {
				return item
			}
			joined = value.ID
		}
		if repository.UpdateLANOutgoing(ctx, item.ID, store.LANApproved, joined, item.Address) == nil {
			item.Status, item.JoinedID = store.LANApproved, joined
		}
	case store.LANDenied, store.LANExpired:
		if repository.UpdateLANOutgoing(ctx, item.ID, reply.Status, item.JoinedID, item.Address) == nil {
			item.Status = reply.Status
		}
	}
	return item
}

func (m *TeamManager) lanRequests(ctx context.Context) (api.LANRequests, error) {
	result := api.LANRequests{Incoming: []api.LANIncoming{}, Outgoing: []store.LANOutgoing{}}
	repository, err := m.lanRepository()
	if err != nil {
		return result, err
	}
	names := map[string]string{}
	if boards, err := m.lanBoards(); err == nil {
		list, _ := boards.LANBoardList(ctx)
		for _, board := range list {
			names[board.ID] = board.Name
		}
	}
	incoming, err := repository.LANRequests(ctx, m.options.Now().Add(-lanRequestTTL).UnixMilli(), m.options.Now().Add(-7*24*time.Hour).UnixMilli())
	if err != nil {
		return result, err
	}
	for _, request := range incoming {
		result.Incoming = append(result.Incoming, api.LANIncoming{LANRequest: request, BoardName: names[request.BoardID]})
	}
	outgoing, err := repository.LANOutgoing(ctx)
	if err != nil {
		return result, err
	}
	var wait sync.WaitGroup
	for index := range outgoing {
		if outgoing[index].Status != store.LANPending {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			check, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			outgoing[index] = m.refreshOutgoing(check, repository, outgoing[index])
		}()
	}
	wait.Wait()
	result.Outgoing = outgoing
	return result, nil
}

func (m *TeamManager) decideLAN(ctx context.Context, id string, approve bool) (api.LANIncoming, error) {
	repository, err := m.lanRepository()
	if err != nil {
		return api.LANIncoming{}, err
	}
	request, err := repository.LANRequest(ctx, id)
	if err != nil {
		return api.LANIncoming{}, missingAPI("申请 " + id + " 不存在")
	}
	if request.Status != store.LANPending || request.CreatedAt < m.options.Now().Add(-lanRequestTTL).UnixMilli() {
		return api.LANIncoming{}, &api.Fault{Status: 409, Code: "lan_decided", Message: "这个申请已经处理过或已过期"}
	}
	if !approve {
		if err := repository.DecideLANRequest(ctx, id, store.LANDenied, request.MemberID, nil); err != nil {
			return api.LANIncoming{}, err
		}
		request.Status = store.LANDenied
		return api.LANIncoming{LANRequest: request}, nil
	}
	if err := m.EnsureLANTeam(ctx); err != nil {
		return api.LANIncoming{}, err
	}
	memberID, certificate := request.MemberID, []byte(nil)
	if len(request.CSR) > 0 {
		var issued string
		issued, certificate, err = m.options.Authority.SignTeamCSR(request.CSR)
		if err != nil {
			return api.LANIncoming{}, invalidAPI("对方的设备证书申请无效")
		}
		leaf, err := teamCertificate(certificate)
		if err != nil {
			return api.LANIncoming{}, err
		}
		if _, err := repository.AddTeamMember(ctx, store.TeamMember{ID: issued, TeamID: lanTeamID, Name: request.DeviceName, Serial: leaf.SerialNumber.String()}); err != nil {
			return api.LANIncoming{}, err
		}
		memberID = issued
	}
	grantID, err := transport.NewTeamID("grant", m.options.Random)
	if err != nil {
		return api.LANIncoming{}, err
	}
	if _, err := repository.AddLANGrant(ctx, store.LANGrant{ID: grantID, MemberID: memberID, BoardID: request.BoardID, DeviceName: request.DeviceName, Avatar: request.Avatar, Hostname: request.Hostname}); err != nil {
		return api.LANIncoming{}, err
	}
	if err := repository.DecideLANRequest(ctx, id, store.LANApproved, memberID, certificate); err != nil {
		return api.LANIncoming{}, err
	}
	request.Status, request.MemberID = store.LANApproved, memberID
	m.notify()
	return api.LANIncoming{LANRequest: request}, nil
}

// remoteRead reads a path of an approved board from its owner with this computer's identity. A
// refusal means the owner revoked the board: it is forgotten here too, so it leaves the sidebar
// and no stale copy is shown.
func (m *TeamManager) remoteRead(ctx context.Context, id, path string, output any) (store.LANOutgoing, error) {
	repository, err := m.lanRepository()
	if err != nil {
		return store.LANOutgoing{}, err
	}
	item, err := repository.LANOutgoingItem(ctx, id)
	if err != nil || item.Status != store.LANApproved || item.JoinedID == "" {
		return item, missingAPI("还没有获准查看这个面板")
	}
	joined, err := m.options.Repository.JoinedTeam(ctx, item.JoinedID)
	if err != nil {
		return item, missingAPI("本机保存的身份已移除，请重新申请")
	}
	configuration, err := transport.LoadTeamTLS(ctx, m.options.Keys, joined.CA, joined.Certificate, joined.KeyRef, joined.Fingerprint, m.options.Now)
	if err != nil {
		return item, err
	}
	err = m.options.Connector.Exchange(ctx, item.Address, "/team/v1/boards/"+item.BoardID+path, configuration, nil, output)
	var remote *transport.TeamRemoteError
	switch {
	case err == nil:
		return item, nil
	case errors.As(err, &remote) && remote.Code == "lan_forbidden":
		if repository.UpdateLANOutgoing(ctx, item.ID, store.LANRevoked, item.JoinedID, item.Address) == nil {
			m.notify()
		}
		return item, &api.Fault{Status: 403, Code: "lan_forbidden", Message: "对方已撤销授权，不能再查看这个面板；需要时重新申请"}
	case errors.As(err, &remote) && remote.Code == "lan_monitor_missing":
		return item, &api.Fault{Status: 404, Code: remote.Code, Message: remote.Message}
	case errors.As(err, &remote) && remote.Code == "team_not_found":
		return item, &api.Fault{Status: 502, Code: "lan_outdated", Message: "对方的 lgmon 版本较旧，升级到 0.13.2 或更新版本后才能查看服务详情和文档"}
	}
	return item, lanRemoteFault(err)
}

func (m *TeamManager) remoteBoard(ctx context.Context, id string) (api.LANRemoteBoard, error) {
	var reply lanBoardReply
	item, err := m.remoteRead(ctx, id, "", &reply)
	if err != nil {
		var fault *api.Fault
		if errors.As(err, &fault) && fault.Code == "lan_rate_limited" {
			m.lan.mu.Lock()
			cached, ok := m.lan.remote[id]
			m.lan.mu.Unlock()
			if age := m.options.Now().Sub(cached.at); ok && age >= 0 && age < remoteFresh {
				return cached.value, nil
			}
		}
		return api.LANRemoteBoard{ID: item.ID, OwnerName: item.OwnerName, OwnerAvatar: item.OwnerAvatar, Address: item.Address}, err
	}
	result := api.LANRemoteBoard{ID: item.ID, OwnerName: item.OwnerName, OwnerAvatar: item.OwnerAvatar, Address: item.Address, Board: reply.BoardView, FetchedAt: m.options.Now().UnixMilli()}
	if owner := reply.Owner; owner != nil && strings.TrimSpace(owner.Name) != "" {
		name, avatar := strings.TrimSpace(owner.Name), remoteAvatar(owner.Avatar)
		if utf8.RuneCountInString(name) > 40 {
			name = string([]rune(name)[:40])
		}
		if repository, err := m.lanRepository(); err == nil && (name != item.OwnerName || avatar != item.OwnerAvatar) && repository.UpdateLANOutgoingOwner(ctx, item.ID, name, avatar) == nil {
			// The sidebar and the small window list remote boards with the owner's face; tell them.
			m.notify()
		}
		result.OwnerName, result.OwnerAvatar = name, avatar
	}
	m.lan.mu.Lock()
	if m.lan.remote == nil {
		m.lan.remote = map[string]remoteCopy{}
	}
	m.lan.remote[id] = remoteCopy{at: m.options.Now(), value: result}
	m.lan.mu.Unlock()
	return result, nil
}

func (m *TeamManager) remoteMonitor(ctx context.Context, id, monitor string) (api.MonitorDetail, error) {
	var detail api.MonitorDetail
	if !validMonitorID(monitor) {
		return detail, missingAPI("服务 ID 无效")
	}
	_, err := m.remoteRead(ctx, id, "/monitors/"+url.PathEscape(monitor), &detail)
	return detail, err
}

func (m *TeamManager) remoteDoc(ctx context.Context, id, monitor string) (api.MonitorDoc, error) {
	var doc api.MonitorDoc
	if !validMonitorID(monitor) {
		return doc, missingAPI("服务 ID 无效")
	}
	_, err := m.remoteRead(ctx, id, "/monitors/"+url.PathEscape(monitor)+"/doc", &doc)
	return doc, err
}

// validMonitorID keeps a path segment a plain ID before it goes into another computer's URL.
func validMonitorID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func (m *TeamManager) lanOperation(ctx context.Context, operation api.Operation, r api.Request) (any, error) {
	repository, err := m.lanRepository()
	if err != nil {
		return nil, err
	}
	switch operation {
	case api.DiscoverLAN:
		return m.discover(ctx)
	case api.LANDirectory:
		address, err := normalizeLANAddress(r.Query.Get("address"))
		if err != nil {
			return nil, err
		}
		directory, _, _, err := m.directory(ctx, address)
		return directory, err
	case api.RequestLAN:
		var input api.LANRequestCreate
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		return m.requestLAN(ctx, input)
	case api.ListLANRequest:
		return m.lanRequests(ctx)
	case api.ApproveLAN, api.DenyLAN:
		return m.decideLAN(ctx, r.Params["id"], operation == api.ApproveLAN)
	case api.CancelLAN:
		if err := repository.DeleteLANOutgoing(ctx, r.Params["id"]); err != nil {
			return nil, missingAPI("这条申请记录不存在")
		}
		return api.Confirmation{OK: true, Message: "已删除这条申请记录"}, nil
	case api.ListLANGrants:
		grants, err := repository.LANGrants(ctx)
		if err != nil {
			return nil, err
		}
		names := map[string]string{}
		if boards, err := m.lanBoards(); err == nil {
			list, _ := boards.LANBoardList(ctx)
			for _, board := range list {
				names[board.ID] = board.Name
			}
		}
		result := make([]api.LANGrantItem, 0, len(grants))
		for _, grant := range grants {
			result = append(result, api.LANGrantItem{LANGrant: grant, BoardName: names[grant.BoardID]})
		}
		return result, nil
	case api.RevokeLANGrant:
		grant, err := repository.RevokeLANGrant(ctx, r.Params["id"])
		if err != nil {
			return nil, missingAPI("这条授权不存在或已撤销")
		}
		m.cancelAccess(grant.MemberID, lanTeamID, "")
		return api.Confirmation{OK: true, Message: grant.DeviceName + " 不能再查看这个面板了"}, nil
	case api.ListLANRemote:
		outgoing, err := repository.LANOutgoing(ctx)
		if err != nil {
			return nil, err
		}
		return slices.DeleteFunc(outgoing, func(item store.LANOutgoing) bool { return item.Status != store.LANApproved }), nil
	case api.GetLANRemote:
		return m.remoteBoard(ctx, r.Params["id"])
	case api.GetLANRemoteMonitor:
		return m.remoteMonitor(ctx, r.Params["id"], r.Params["monitor"])
	case api.GetLANRemoteDoc:
		return m.remoteDoc(ctx, r.Params["id"], r.Params["monitor"])
	}
	return nil, invalidAPI("不支持此内网操作")
}

// LANAddresses lists addresses others can type to reach this computer's member port.
func LANAddresses(listening string) []string {
	_, port, err := net.SplitHostPort(listening)
	if err != nil {
		return []string{}
	}
	number, _ := strconv.Atoi(port)
	host, _, _ := net.SplitHostPort(listening)
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		return []string{transport.LANAddress(ip, number)}
	}
	result := []string{}
	interfaces, _ := net.Interfaces()
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := item.Addrs()
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLinkLocalUnicast() {
				result = append(result, transport.LANAddress(network.IP, number))
			}
		}
	}
	return result
}

// LANListening reports the member port's listen address and the last failure, for the status page.
func (m *TeamManager) LANListening() (string, error) {
	m.mu.Lock()
	listening := m.listening
	m.mu.Unlock()
	if listening == nil {
		return "", nil
	}
	return listening()
}

// LANState is the status page's view of LAN sharing on this computer.
func (m *TeamManager) LANState(ctx context.Context) (api.LANState, error) {
	address, failure := m.LANListening()
	settings := m.options.Config.Snapshot().Settings
	addresses := []string{}
	problem := ""
	if settings.LAN.Visible {
		if address != "" {
			addresses = LANAddresses(address)
			if host, _, _ := net.SplitHostPort(address); host == "127.0.0.1" || host == "::1" {
				problem = "成员端口只监听本机（settings.yaml 的 teamListen），内网里的其他电脑连不上"
			}
		} else if failure != nil {
			problem = "成员端口没有打开：" + failure.Error()
		} else {
			problem = "正在打开成员端口…"
		}
	}
	state, err := m.lanState(ctx, address != "", problem, addresses)
	m.mu.Lock()
	firewall := m.firewall
	m.mu.Unlock()
	if settings.LAN.Visible && firewall != nil && m.options.Firewall != nil {
		current := firewall()
		switch {
		case current.applied:
			state.Firewall = "allowed"
		case current.want != nil && current.err != nil:
			state.Firewall, state.FirewallFix = "blocked", lanFirewallFix(*current.want)
			if state.Problem == "" {
				state.Problem = "Windows 防火墙还没有放行 lgmon，内网里的其他电脑搜不到你、也发不来申请。用安装版（以系统服务运行）打开内网可见会自动放行；现在可以在管理员 PowerShell 里运行下面的命令。"
			}
		}
	}
	return state, err
}
