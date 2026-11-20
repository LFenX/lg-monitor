package app

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/profile"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

/*
 * Departments on the LAN. The computer that creates a department hosts it and answers for it on the network even
 * when its boards are not shared; everyone else finds it by discovery and reads it from there. Joining, approving,
 * naming leaders and leaving are requests signed with each computer's account key, so any leader can approve from
 * their own computer. Departments may nest, also under one another computer hosts.
 */

type departmentRepository interface {
	AccountKey(context.Context) (ed25519.PrivateKey, error)
	Departments(context.Context) ([]store.Department, error)
	Department(context.Context, string) (store.Department, error)
	SaveDepartment(context.Context, store.Department, *store.DepartmentPerson) (store.Department, error)
	DeleteDepartment(context.Context, string) error
	DepartmentPeople(context.Context, string) ([]store.DepartmentPerson, error)
	SetDepartmentPerson(context.Context, store.DepartmentPerson) error
	RemoveDepartmentPerson(context.Context, string, string) error
	AddDepartmentRequest(context.Context, store.DepartmentRequest) (store.DepartmentRequest, error)
	DepartmentRequests(context.Context, string) ([]store.DepartmentRequest, error)
	DepartmentRequest(context.Context, string) (store.DepartmentRequest, error)
	DecideDepartmentRequest(context.Context, string, string) error
}

// departmentState remembers where hosts were found and how to reach them, briefly.
type departmentState struct {
	mu      sync.Mutex
	found   time.Time
	hosts   []api.DepartmentHost
	reach   map[string]departmentReach
	pending map[string]bool
}

type departmentReach struct {
	address string
	config  *tls.Config
	at      time.Time
}

const signedFresh = 5 * time.Minute

func (m *TeamManager) departments() (departmentRepository, error) {
	repository, ok := m.options.Repository.(departmentRepository)
	if !ok {
		return nil, &api.Fault{Status: 503, Code: "department_unavailable", Message: "部门需要新版认证库"}
	}
	return repository, nil
}

// account is this computer's person: its key, and the nickname and avatar it goes by.
func (m *TeamManager) person(ctx context.Context) (ed25519.PrivateKey, transport.DepartmentPerson, error) {
	repository, err := m.departments()
	if err != nil {
		return nil, transport.DepartmentPerson{}, err
	}
	if _, err := m.EnsureProfile(ctx); err != nil {
		return nil, transport.DepartmentPerson{}, err
	}
	key, err := repository.AccountKey(ctx)
	if err != nil {
		return nil, transport.DepartmentPerson{}, err
	}
	return key, transport.DepartmentPerson{Key: accountID(key.Public().(ed25519.PublicKey)), Name: m.lanName(), Avatar: m.lanAvatar()}, nil
}

func accountID(key ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(key) }

// AccountID is this computer's account key, as other computers see it.
func (m *TeamManager) AccountID(ctx context.Context) string {
	_, me, err := m.person(ctx)
	if err != nil {
		return ""
	}
	return me.Key
}

// hostID names this computer as discovery does: the first 16 characters of its certificate fingerprint.
func (m *TeamManager) hostID() string {
	return strings.ToUpper(m.options.Authority.Fingerprint()[:16])
}

func checkDepartmentName(value string) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || utf8.RuneCountInString(value) > 40 {
		return "", invalidAPI("部门名称要 1 到 40 个字")
	}
	return value, nil
}

// ---------- the hosting computer ----------

func (m *TeamManager) departmentCards(ctx context.Context) ([]transport.DepartmentCard, error) {
	repository, err := m.departments()
	if err != nil {
		return nil, err
	}
	list, err := repository.Departments(ctx)
	if err != nil {
		return nil, err
	}
	cards := make([]transport.DepartmentCard, 0, len(list))
	for _, item := range list {
		people, err := repository.DepartmentPeople(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		card := transport.DepartmentCard{ID: item.ID, Name: item.Name, Avatar: item.Avatar, Parent: item.Parent, People: []transport.DepartmentPerson{}}
		for _, person := range people {
			card.People = append(card.People, transport.DepartmentPerson{Key: person.Key, Name: person.Name, Avatar: person.Avatar, Role: person.Role})
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func (m *TeamManager) serveDepartments(w http.ResponseWriter, r *http.Request) {
	if !m.options.Config.Snapshot().Settings.LAN.Present() {
		writeTeamError(w, &api.Fault{Status: 404, Code: "department_none", Message: "对方没有托管部门"})
		return
	}
	cards, err := m.departmentCards(r.Context())
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeTeam(w, 200, transport.DepartmentDirectory{Host: transport.LANOwner{Name: m.lanName(), Avatar: m.lanAvatar(), Account: m.AccountID(r.Context())}, Fingerprint: m.options.Authority.Fingerprint(), Departments: cards})
}

// verifySigned checks a department request's signature and freshness and returns who sent it and its data.
func (m *TeamManager) verifySigned(path string, body []byte) (string, json.RawMessage, error) {
	var request transport.SignedRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return "", nil, invalidAPI("请求格式无效")
	}
	key, err := base64.StdEncoding.DecodeString(request.Account)
	signature, sigErr := base64.StdEncoding.DecodeString(request.Signature)
	if err != nil || sigErr != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, transport.SignedText(path, request.Time, request.Data), signature) {
		return "", nil, &api.Fault{Status: 403, Code: "department_signature", Message: "请求签名不对"}
	}
	at := time.UnixMilli(request.Time)
	if age := m.options.Now().Sub(at); age > signedFresh || age < -signedFresh {
		return "", nil, &api.Fault{Status: 403, Code: "department_signature", Message: "请求已过期，请核对两台电脑的时间"}
	}
	return request.Account, request.Data, nil
}

type departmentAction = api.DepartmentActionInput

func (m *TeamManager) serveDepartmentAction(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 96*1024))
	if err != nil {
		writeTeamError(w, invalidAPI("请求不可读"))
		return
	}
	who, data, err := m.verifySigned(r.URL.Path, body)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	if !m.allowRate("department:"+who+":"+r.PathValue("action"), 200*time.Millisecond) {
		writeTeamError(w, &api.Fault{Status: 429, Code: "lan_rate_limited", Message: "请求太频繁了，请稍后再试"})
		return
	}
	var input departmentAction
	if len(data) > 0 && json.Unmarshal(data, &input) != nil {
		writeTeamError(w, invalidAPI("请求内容无效"))
		return
	}
	result, err := m.departmentAct(r.Context(), r.PathValue("id"), r.PathValue("action"), who, input)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeTeam(w, 200, result)
}

// departmentAct does one action on a department hosted here, as the person with account key who.
func (m *TeamManager) departmentAct(ctx context.Context, id, action, who string, input departmentAction) (any, error) {
	repository, err := m.departments()
	if err != nil {
		return nil, err
	}
	department, err := repository.Department(ctx, id)
	if err != nil {
		return nil, missingAPI("部门不存在或已被删除")
	}
	people, err := repository.DepartmentPeople(ctx, id)
	if err != nil {
		return nil, err
	}
	role := ""
	leaders := 0
	for _, person := range people {
		if person.Key == who {
			role = person.Role
		}
		if person.Role == store.DepartmentLeader {
			leaders++
		}
	}
	leader := func() error {
		if role != store.DepartmentLeader {
			return &api.Fault{Status: 403, Code: "department_forbidden", Message: "只有部门负责人可以这样做"}
		}
		return nil
	}
	switch action {
	case "status":
		if role == "" {
			requests, _ := repository.DepartmentRequests(ctx, id)
			if slices.ContainsFunc(requests, func(item store.DepartmentRequest) bool { return item.Key == who }) {
				role = store.LANPending
			}
		}
		return map[string]string{"role": role}, nil
	case "join":
		if role != "" {
			return map[string]string{"role": role}, nil
		}
		name, err := profile.CheckNickname(input.Name)
		if err != nil {
			return nil, invalidAPI(err.Error())
		}
		requestID, err := transport.NewTeamID("deptreq", m.options.Random)
		if err != nil {
			return nil, err
		}
		if _, err := repository.AddDepartmentRequest(ctx, store.DepartmentRequest{ID: requestID, Department: id, Key: who, Name: name, Avatar: remoteAvatar(input.Avatar)}); err != nil {
			return nil, err
		}
		m.notify()
		return map[string]string{"role": store.LANPending}, nil
	case "leave":
		if role == store.DepartmentLeader && leaders == 1 && len(people) > 1 {
			return nil, invalidAPI("你是唯一的负责人，先把另一位成员设为负责人再退出")
		}
		if err := repository.RemoveDepartmentPerson(ctx, id, who); err != nil {
			return nil, err
		}
		m.notify()
		return map[string]string{"role": ""}, nil
	case "requests":
		if err := leader(); err != nil {
			return nil, err
		}
		requests, err := repository.DepartmentRequests(ctx, id)
		return map[string]any{"requests": requests}, err
	case "decide":
		if err := leader(); err != nil {
			return nil, err
		}
		request, err := repository.DepartmentRequest(ctx, input.Request)
		if err != nil || request.Department != id {
			return nil, missingAPI("这条加入申请不存在或已处理")
		}
		status := store.LANDenied
		if input.Approve {
			status = store.LANApproved
			if err := repository.SetDepartmentPerson(ctx, store.DepartmentPerson{Department: id, Key: request.Key, Name: request.Name, Avatar: request.Avatar, Role: store.DepartmentMember}); err != nil {
				return nil, err
			}
		}
		if err := repository.DecideDepartmentRequest(ctx, request.ID, status); err != nil {
			return nil, missingAPI("这条加入申请已经处理过了")
		}
		m.notify()
		return map[string]string{"status": status}, nil
	case "role":
		if err := leader(); err != nil {
			return nil, err
		}
		index := slices.IndexFunc(people, func(person store.DepartmentPerson) bool { return person.Key == input.Key })
		if index < 0 || (input.Role != store.DepartmentLeader && input.Role != store.DepartmentMember) {
			return nil, invalidAPI("只能把部门里的人设为负责人或成员")
		}
		if people[index].Role == store.DepartmentLeader && input.Role == store.DepartmentMember && leaders == 1 {
			return nil, invalidAPI("部门至少要有一位负责人")
		}
		person := people[index]
		person.Role = input.Role
		if err := repository.SetDepartmentPerson(ctx, person); err != nil {
			return nil, err
		}
		m.notify()
		return map[string]string{"role": person.Role}, nil
	case "remove":
		if err := leader(); err != nil {
			return nil, err
		}
		index := slices.IndexFunc(people, func(person store.DepartmentPerson) bool { return person.Key == input.Key })
		if index < 0 {
			return nil, missingAPI("这个人不在部门里")
		}
		if people[index].Role == store.DepartmentLeader && leaders == 1 {
			return nil, invalidAPI("部门至少要有一位负责人")
		}
		if err := repository.RemoveDepartmentPerson(ctx, id, input.Key); err != nil {
			return nil, err
		}
		m.notify()
		return map[string]bool{"ok": true}, nil
	case "edit":
		if err := leader(); err != nil {
			return nil, err
		}
		if input.Name != "" {
			if department.Name, err = checkDepartmentName(input.Name); err != nil {
				return nil, err
			}
		}
		if input.Avatar != "" {
			if err := profile.CheckAvatar(input.Avatar); err != nil {
				return nil, invalidAPI(err.Error())
			}
			department.Avatar = input.Avatar
		}
		if input.Parent != nil && strings.TrimSpace(*input.Parent) != department.Parent {
			parent := strings.TrimSpace(*input.Parent)
			if parent != "" {
				host, parentID, ok := strings.Cut(parent, ":")
				if !ok || host == "" || parentID == "" {
					return nil, invalidAPI("上级部门的写法不对")
				}
				if under, err := m.underDepartment(ctx, parent, id); err != nil {
					return nil, err
				} else if under {
					return nil, invalidAPI("不能挂在自己或自己的下级部门下面")
				}
			}
			department.Parent = parent
		}
		saved, err := repository.SaveDepartment(ctx, department, nil)
		if err != nil {
			return nil, err
		}
		m.notify()
		return saved, nil
	}
	return nil, invalidAPI("不支持这个部门操作")
}

// underDepartment reports whether parent is the department id itself or sits somewhere below it, following the
// departments this computer hosts. A chain through another computer's department is not followed; the page
// offers only places outside the department's own branch anyway.
func (m *TeamManager) underDepartment(ctx context.Context, parent, id string) (bool, error) {
	repository, err := m.departments()
	if err != nil {
		return false, err
	}
	list, err := repository.Departments(ctx)
	if err != nil {
		return false, err
	}
	parents := map[string]string{}
	for _, item := range list {
		parents[m.hostID()+":"+item.ID] = item.Parent
	}
	self := m.hostID() + ":" + id
	for at, steps := parent, 0; at != "" && steps <= len(list); at, steps = parents[at], steps+1 {
		if at == self {
			return true, nil
		}
	}
	return false, nil
}

// ---------- every computer: finding departments and acting on them ----------

// departmentHosts finds computers hosting departments, at most every 30 seconds.
func (m *TeamManager) departmentHosts(ctx context.Context) []api.DepartmentHost {
	m.dept.mu.Lock()
	if age := m.options.Now().Sub(m.dept.found); !m.dept.found.IsZero() && age >= 0 && age < 30*time.Second {
		hosts := slices.Clone(m.dept.hosts)
		m.dept.mu.Unlock()
		return hosts
	}
	m.dept.mu.Unlock()
	peers, err := m.discoverAll(ctx)
	hosts := []api.DepartmentHost{}
	if err == nil {
		var wait sync.WaitGroup
		var mu sync.Mutex
		for _, peer := range peers {
			if !peer.Departments {
				continue
			}
			wait.Add(1)
			go func() {
				defer wait.Done()
				look, cancel := context.WithTimeout(ctx, 4*time.Second)
				defer cancel()
				directory, config, err := m.departmentDirectory(look, peer.Address, peer.Fingerprint)
				if err != nil {
					return
				}
				mu.Lock()
				hosts = append(hosts, api.DepartmentHost{ID: peer.Fingerprint[:16], Address: peer.Address, Name: directory.Host.Name, Avatar: remoteAvatar(directory.Host.Avatar), Departments: directory.Departments})
				mu.Unlock()
				m.dept.mu.Lock()
				if m.dept.reach == nil {
					m.dept.reach = map[string]departmentReach{}
				}
				m.dept.reach[peer.Fingerprint[:16]] = departmentReach{address: peer.Address, config: config, at: m.options.Now()}
				m.dept.mu.Unlock()
			}()
		}
		wait.Wait()
	}
	m.dept.mu.Lock()
	m.dept.found, m.dept.hosts = m.options.Now(), hosts
	m.dept.mu.Unlock()
	return slices.Clone(hosts)
}

func (m *TeamManager) departmentDirectory(ctx context.Context, address, fingerprint string) (transport.DepartmentDirectory, *tls.Config, error) {
	var directory transport.DepartmentDirectory
	caPEM, seen, err := transport.FetchTeamAuthority(ctx, address, m.options.Connector, m.options.Now)
	if err != nil || !strings.HasPrefix(strings.ToUpper(seen), strings.ToUpper(fingerprint)) {
		return directory, nil, &api.Fault{Status: 502, Code: "department_unreachable", Message: "连不上托管这个部门的电脑"}
	}
	config, err := transport.PinnedTLS(caPEM, m.options.Now)
	if err != nil {
		return directory, nil, err
	}
	if err := m.options.Connector.Exchange(ctx, address, "/team/v1/departments", config, nil, &directory); err != nil {
		return directory, nil, lanRemoteFault(err)
	}
	return directory, config, nil
}

// discoverAll is LAN discovery without leaving out computers that only host departments.
func (m *TeamManager) discoverAll(ctx context.Context) ([]transport.LANPeer, error) {
	packets := m.options.Packets
	if packets == nil {
		packets = NativePacketNetwork{}
	}
	conn, err := packets.ListenPacket(ctx, "0.0.0.0:0")
	if err != nil {
		return nil, err
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
		return nil, err
	}
	own := m.options.Authority.Fingerprint()
	return slices.DeleteFunc(peers, func(peer transport.LANPeer) bool { return strings.HasPrefix(own, peer.Fingerprint) }), nil
}

// signedCall asks the computer hosting a department to do something, signed with this computer's key.
func (m *TeamManager) signedCall(ctx context.Context, host, id, action string, input departmentAction, output any) error {
	m.dept.mu.Lock()
	reach, ok := m.dept.reach[host]
	m.dept.mu.Unlock()
	if !ok {
		m.departmentHosts(ctx)
		m.dept.mu.Lock()
		reach, ok = m.dept.reach[host]
		m.dept.mu.Unlock()
		if !ok {
			return &api.Fault{Status: 502, Code: "department_unreachable", Message: "找不到托管这个部门的电脑，它可能不在线"}
		}
	}
	key, _, err := m.person(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	path := "/team/v1/departments/" + id + "/" + action
	at := m.options.Now().UnixMilli()
	request := transport.SignedRequest{Account: accountID(key.Public().(ed25519.PublicKey)), Time: at, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, transport.SignedText(path, at, data))), Data: data}
	if err := m.options.Connector.Exchange(ctx, reach.address, path, reach.config, request, output); err != nil {
		var remote *transport.TeamRemoteError
		if errors.As(err, &remote) {
			return &api.Fault{Status: remote.Status, Code: remote.Code, Message: remote.Message}
		}
		return lanRemoteFault(err)
	}
	return nil
}

// departmentList is every department this computer can see: its own, then those found on the LAN, each with
// where this computer's person stands in it.
func (m *TeamManager) departmentList(ctx context.Context) (api.DepartmentList, error) {
	_, me, err := m.person(ctx)
	if err != nil {
		return api.DepartmentList{}, err
	}
	result := api.DepartmentList{Me: me.Key, Hosts: []api.DepartmentHost{}}
	cards, err := m.departmentCards(ctx)
	if err != nil {
		return result, err
	}
	if len(cards) > 0 {
		result.Hosts = append(result.Hosts, api.DepartmentHost{ID: m.hostID(), Local: true, Name: me.Name, Avatar: me.Avatar, Departments: cards})
	}
	result.Hosts = append(result.Hosts, m.departmentHosts(ctx)...)
	result.Pending = map[string]string{}
	repository, _ := m.departments()
	for _, host := range result.Hosts {
		for _, card := range host.Departments {
			if slices.ContainsFunc(card.People, func(person transport.DepartmentPerson) bool { return person.Key == me.Key }) {
				continue
			}
			key := host.ID + ":" + card.ID
			if host.Local {
				if requests, err := repository.DepartmentRequests(ctx, card.ID); err == nil && slices.ContainsFunc(requests, func(item store.DepartmentRequest) bool { return item.Key == me.Key }) {
					result.Pending[key] = store.LANPending
				}
				continue
			}
			m.dept.mu.Lock()
			asked := m.dept.pending[key]
			m.dept.mu.Unlock()
			if asked {
				result.Pending[key] = store.LANPending
			}
		}
	}
	return result, nil
}

// departmentOperation runs the local API for departments.
func (m *TeamManager) departmentOperation(ctx context.Context, operation api.Operation, r api.Request) (any, error) {
	repository, err := m.departments()
	if err != nil {
		return nil, err
	}
	switch operation {
	case api.ListDepartments:
		return m.departmentList(ctx)
	case api.CreateDepartment:
		var input api.DepartmentInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		name, err := checkDepartmentName(input.Name)
		if err != nil {
			return nil, err
		}
		avatar := strings.TrimSpace(input.Avatar)
		if avatar == "" {
			if avatar, err = profile.RandomAvatar(m.options.Random, ""); err != nil {
				return nil, err
			}
		} else if err := profile.CheckAvatar(avatar); err != nil {
			return nil, invalidAPI(err.Error())
		}
		id, err := transport.NewTeamID("dept", m.options.Random)
		if err != nil {
			return nil, err
		}
		_, me, err := m.person(ctx)
		if err != nil {
			return nil, err
		}
		saved, err := repository.SaveDepartment(ctx, store.Department{ID: id, Name: name, Avatar: avatar, Parent: strings.TrimSpace(input.Parent)}, &store.DepartmentPerson{Key: me.Key, Name: me.Name, Avatar: me.Avatar})
		if err != nil {
			return nil, err
		}
		m.notify()
		return saved, nil
	case api.DeleteDepartment:
		if err := repository.DeleteDepartment(ctx, r.Params["id"]); err != nil {
			return nil, err
		}
		m.notify()
		return api.Confirmation{OK: true, Message: "部门已删除"}, nil
	case api.DepartmentAct:
		var input departmentAction
		if len(r.Body) > 0 {
			if err := json.Unmarshal(r.Body, &input); err != nil {
				return nil, invalidAPI("请求内容无效")
			}
		}
		host, id, action := r.Params["host"], r.Params["id"], r.Params["action"]
		_, me, err := m.person(ctx)
		if err != nil {
			return nil, err
		}
		if action == "join" {
			input.Name, input.Avatar = me.Name, me.Avatar
		}
		if host == m.hostID() || host == "local" {
			return m.departmentAct(ctx, id, action, me.Key, input)
		}
		var result any
		if err := m.signedCall(ctx, host, id, action, input, &result); err != nil {
			return nil, err
		}
		if action == "join" || action == "leave" {
			m.dept.mu.Lock()
			if m.dept.pending == nil {
				m.dept.pending = map[string]bool{}
			}
			m.dept.pending[host+":"+id] = action == "join"
			m.dept.found = time.Time{}
			m.dept.mu.Unlock()
		}
		if action == "decide" || action == "role" || action == "remove" || action == "edit" {
			m.dept.mu.Lock()
			m.dept.found = time.Time{}
			m.dept.mu.Unlock()
		}
		return result, nil
	}
	return nil, invalidAPI("不支持此部门操作")
}

// hostsDepartments reports whether this computer hosts any department now.
func (m *TeamManager) hostsDepartments(ctx context.Context) bool {
	repository, err := m.departments()
	if err != nil {
		return false
	}
	list, err := repository.Departments(ctx)
	return err == nil && len(list) > 0
}
