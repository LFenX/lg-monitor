// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

// LAN sharing: a computer may announce its boards on the local network; reading one needs a
// request, a matching pairing code on both screens, and the owner's approval.
const (
	RevealLANKey       Operation = "lan_key_reveal"
	GetLANKey          Operation = "lan_key_status"
	ChangeLANKeyExpiry Operation = "lan_key_expiry"
	GenerateLANKey     Operation = "lan_key_generate"
	RevokeLANKey       Operation = "lan_key_revoke"
	UnlockLAN          Operation = "lan_unlock"
	GetLAN             Operation = "lan_status"
	ConfigureLAN       Operation = "lan_configure"
	DiscoverLAN        Operation = "lan_discover"
	LANDirectory       Operation = "lan_directory"
	RequestLAN         Operation = "lan_request"
	ListLANRequest     Operation = "lan_requests"
	ApproveLAN         Operation = "lan_approve"
	DenyLAN            Operation = "lan_deny"
	CancelLAN          Operation = "lan_cancel"
	ListLANGrants      Operation = "lan_grants"
	RevokeLANGrant     Operation = "lan_revoke"
	ListLANRemote      Operation = "lan_remote"
	GetLANRemote       Operation = "lan_remote_board"
	// GetLANRemoteMonitor and GetLANRemoteDoc open one service of a shared board, read-only.
	GetLANRemoteMonitor Operation = "lan_remote_monitor"
	GetLANRemoteDoc     Operation = "lan_remote_doc"
	GetProfile          Operation = "profile"
	// ListModelShares and SetModelShare decide which paired colleagues may use this computer's model.
	ListModelShares Operation = "assistant_shares"
	// Departments: groups people on the LAN make, hosted by the computer that created each one.
	ListDepartments  Operation = "departments"
	CreateDepartment Operation = "department_create"
	DeleteDepartment Operation = "department_delete"
	DepartmentAct    Operation = "department_act"
	SetModelShare    Operation = "assistant_share"
	UpdateProfile    Operation = "profile_update"
	ListLANVisitors  Operation = "lan_visitors"
)

// DepartmentHost is a computer hosting departments, and what it hosts.
type DepartmentHost struct {
	ID          string                     `json:"id" jsonschema_description:"托管这些部门的电脑（证书指纹前 16 位）。"`
	Address     string                     `json:"address,omitempty"`
	Local       bool                       `json:"local,omitempty" jsonschema_description:"true 表示就是这台电脑。"`
	Name        string                     `json:"name" jsonschema_description:"托管电脑的主人昵称。"`
	Avatar      string                     `json:"avatar,omitempty"`
	Departments []transport.DepartmentCard `json:"departments"`
}

type DepartmentList struct {
	Me    string           `json:"me" jsonschema_description:"这台电脑的账户密钥，用来在成员里认出自己。"`
	Hosts []DepartmentHost `json:"hosts"`
	// Pending names departments ("托管电脑:部门ID") this computer asked to join and waits for.
	Pending map[string]string `json:"pending"`
}

type DepartmentInput struct {
	Name   string `json:"name" jsonschema:"minLength=1,maxLength=40"`
	Avatar string `json:"avatar,omitempty" jsonschema:"maxLength=65536" jsonschema_description:"内置头像名或 data URL 图片；省略时随机。"`
	Parent string `json:"parent,omitempty" jsonschema:"maxLength=120" jsonschema_description:"上级部门：托管电脑:部门ID；省略时是顶层部门。"`
}

// DepartmentActionInput is one action on a department; which fields count depends on the action.
type DepartmentActionInput struct {
	Name   string `json:"name,omitempty" jsonschema:"maxLength=40"`
	Avatar string `json:"avatar,omitempty" jsonschema:"maxLength=65536"`
	// Parent is a pointer so an edit that leaves it out keeps the department where it is; "" makes it top level.
	Parent  *string `json:"parent,omitempty" jsonschema:"maxLength=120" jsonschema_description:"edit：新的上级部门（托管电脑:部门ID），空字符串表示改为顶层；省略时不变。"`
	Request string  `json:"request,omitempty" jsonschema_description:"decide：加入申请 ID。"`
	Approve bool    `json:"approve,omitempty"`
	Key     string  `json:"key,omitempty" jsonschema_description:"role、remove：成员的账户密钥。"`
	Role    string  `json:"role,omitempty" jsonschema:"enum=leader,enum=member,enum="`
}

// ModelShareItem is a paired colleague and whether 波西米亚's model is shared with them.
type ModelShareItem struct {
	MemberID string `json:"memberId"`
	Name     string `json:"name" jsonschema_description:"对方的昵称。"`
	Avatar   string `json:"avatar,omitempty"`
	Shared   bool   `json:"shared" jsonschema_description:"true 表示对方的波西米亚可以用这台电脑的模型密钥（请求经这台电脑转发，密钥不离开这台电脑）。"`
}

type ModelShareInput struct {
	Shared bool `json:"shared"`
}

// ProfileInput changes this computer's account; fields left out keep their value.
type ProfileInput struct {
	Nickname *string `json:"nickname,omitempty" jsonschema:"minLength=1,maxLength=24" jsonschema_description:"别人在内网里看到的昵称。"`
	Avatar   *string `json:"avatar,omitempty" jsonschema:"maxLength=65536" jsonschema_description:"内置头像 fiora-0…fiora-14、huaji-1、huaji-18、huaji-33、huaji-34、huaji-36，或 data:image/(png|jpeg|gif|webp);base64,… 的小图片（64 KiB 以内）。"`
	Shuffle  bool    `json:"shuffle,omitempty" jsonschema_description:"true 时随机换一个和现在不同的内置头像。"`
}

type LANState struct {
	Visible         bool                `json:"visible"`
	Web             bool                `json:"web"`
	WebBoards       []string            `json:"webBoards,omitempty" jsonschema_description:"允许内网网页访客查看的面板 ID 列表；为空表示全部面板可见。"`
	WebServices     map[string][]string `json:"webServices,omitempty"`
	WebDeviceBoards map[string][]string `json:"webDeviceBoards,omitempty" jsonschema_description:"同一云账号下其他设备开放给内网网页访客的面板：设备 ID 对应面板 ID 列表。"`
	WebAddresses    []string            `json:"webAddresses,omitempty" jsonschema_description:"开启内网网页访问后，内网同事可在浏览器打开的只读看板地址。"`
	Name            string              `json:"name" jsonschema_description:"别人在内网里看到的昵称，即本机账户的昵称。"`
	Avatar          string              `json:"avatar,omitempty" jsonschema_description:"本机账户的头像：内置头像的名字或 data URL 图片。"`
	Hostname        string              `json:"hostname"`
	Fingerprint     string              `json:"fingerprint" jsonschema_description:"本机证书指纹的短格式，对方核对用。"`
	Addresses       []string            `json:"addresses" jsonschema_description:"别人手动连接时可以输入的地址。"`
	Listening       bool                `json:"listening" jsonschema_description:"成员端口是否已经在监听。"`
	Problem         string              `json:"problem,omitempty"`
	Firewall        string              `json:"firewall,omitempty" jsonschema:"enum=allowed,enum=blocked" jsonschema_description:"allowed：已在 Windows 防火墙为内网和 Tailscale 地址放行成员端口与发现端口；blocked：没能放行，别人搜不到也连不上。"`
	FirewallFix     string              `json:"firewallFix,omitempty" jsonschema_description:"blocked 时在管理员 PowerShell 里运行的放行命令。"`
	Pending         int                 `json:"pending" jsonschema_description:"等待你处理的申请数。"`
	Viewers         int                 `json:"viewers" jsonschema_description:"正在生效的授权数。"`
}

type LANConfigInput struct {
	Visible         *bool                `json:"visible,omitempty"`
	Web             *bool                `json:"web,omitempty"`
	WebBoards       *[]string            `json:"webBoards,omitempty" jsonschema_description:"允许内网网页访客查看的面板 ID 列表；传空数组表示全部面板可见。"`
	WebServices     *map[string][]string `json:"webServices,omitempty" jsonschema_description:"按面板更新访客可见服务；空数组关闭面板上的所有服务，null 恢复该面板的全部服务。"`
	WebDeviceBoards *map[string][]string `json:"webDeviceBoards,omitempty" jsonschema_description:"按设备更新开放给访客的其他设备面板；给出的列表整体替换该设备的设置，空数组或 null 不再开放该设备的任何面板。"`
	Name            *string              `json:"name,omitempty" jsonschema:"maxLength=24" jsonschema_description:"改本机账户的昵称，等同于 PUT /api/v1/profile 的 nickname。"`
}

type LANPeerResult struct {
	transport.LANPeer
	Directory *transport.LANDirectory `json:"directory,omitempty"`
	Problem   string                  `json:"problem,omitempty"`
}

type LANRequestCreate struct {
	Address     string `json:"address" jsonschema:"minLength=1,maxLength=300" jsonschema_description:"对方地址，例如 192.168.1.20 或 https://192.168.1.20:7445。"`
	BoardID     string `json:"boardId" jsonschema:"minLength=1,maxLength=48"`
	Fingerprint string `json:"fingerprint,omitempty" jsonschema_description:"发现时看到的指纹；手动输入地址时可省略，靠配对码核对。"`
}

type LANIncoming struct {
	store.LANRequest
	BoardName string `json:"boardName"`
}

type LANRequests struct {
	Incoming []LANIncoming       `json:"incoming" jsonschema_description:"别人向这台电脑发来的申请，新的在前。"`
	Outgoing []store.LANOutgoing `json:"outgoing" jsonschema_description:"这台电脑发出的申请。"`
}

type LANGrantItem struct {
	store.LANGrant
	BoardName string `json:"boardName"`
}

type LANRemoteBoard struct {
	ID          string    `json:"id" jsonschema_description:"本机记录的申请 ID，用来读取这个远程面板。"`
	OwnerName   string    `json:"ownerName"`
	OwnerAvatar string    `json:"ownerAvatar,omitempty"`
	Address     string    `json:"address"`
	Board       BoardView `json:"board"`
	FetchedAt   int64     `json:"fetchedAt,omitempty" jsonschema_description:"这份状态从对方读到的时间，UTC Unix 毫秒。"`
}

func LANRoutes() []Route {
	return []Route{
		{GetLAN, "GET", "/api/v1/lan", "内网可见开关、本机地址与指纹、待处理申请数", ScopeRead, nil, LANState{}, nil},
		{ConfigureLAN, "PUT", "/api/v1/lan", "打开或关闭内网可见，修改在内网显示的名字", ScopeConfig, LANConfigInput{}, LANState{}, nil},
		{DiscoverLAN, "POST", "/api/v1/lan/discover", "在局域网里找其他打开了内网可见的 lgmon，并读取它们公开的面板名称和简介", ScopeRead, nil, []LANPeerResult{}, nil},
		{LANDirectory, "GET", "/api/v1/lan/directory", "按地址读取对方公开的面板名称和简介（不能广播的网络用它）", ScopeRead, nil, transport.LANDirectory{}, []Parameter{{"address", "对方地址"}}},
		{RequestLAN, "POST", "/api/v1/lan/requests", "向对方申请查看一个面板，返回双方都会显示的配对码", ScopeConfig, LANRequestCreate{}, store.LANOutgoing{}, nil},
		{ListLANRequest, "GET", "/api/v1/lan/requests", "收到的和发出的申请；顺便向对方确认发出的申请是否已被同意", ScopeRead, nil, LANRequests{}, nil},
		{ApproveLAN, "POST", "/api/v1/lan/requests/{id}/approve", "同意申请：对方之后可以只读查看这个面板。先和对方核对配对码", ScopeConfig, nil, LANIncoming{}, nil},
		{DenyLAN, "POST", "/api/v1/lan/requests/{id}/deny", "拒绝申请", ScopeConfig, nil, LANIncoming{}, nil},
		{CancelLAN, "DELETE", "/api/v1/lan/requests/{id}", "删除本机发出的一条申请记录", ScopeConfig, nil, Confirmation{}, nil},
		{ListLANGrants, "GET", "/api/v1/lan/grants", "谁可以看我的哪个面板", ScopeRead, nil, []LANGrantItem{}, nil},
		{RevokeLANGrant, "DELETE", "/api/v1/lan/grants/{id}", "撤销授权，立即生效", ScopeConfig, nil, Confirmation{}, nil},
		{ListLANRemote, "GET", "/api/v1/lan/remote", "我能看的别人的面板", ScopeRead, nil, []store.LANOutgoing{}, nil},
		{GetLANRemote, "GET", "/api/v1/lan/remote/{id}", "读取一个别人的面板（只读，实时）", ScopeRead, nil, LANRemoteBoard{}, nil},
		{GetLANRemoteMonitor, "GET", "/api/v1/lan/remote/{id}/monitors/{monitor}", "别人面板里一个服务的详情（只读）：状态、可用率、响应时间、子服务、事件、告警规则", ScopeRead, nil, MonitorDetail{}, nil},
		{GetLANRemoteDoc, "GET", "/api/v1/lan/remote/{id}/monitors/{monitor}/doc", "别人面板里一个服务的文档（只读）", ScopeRead, nil, MonitorDoc{}, nil},
		{GetProfile, "GET", "/api/v1/profile", "本机账户：别人在内网里看到的昵称和头像；安装后第一次启动时自动创建", ScopeRead, nil, store.Profile{}, nil},
		{ListDepartments, "GET", "/api/v1/departments", "内网里的部门（这台电脑托管的和局域网里找到的）、成员和负责人，以及这台电脑在每个部门里的身份", ScopeRead, nil, DepartmentList{}, nil},
		{CreateDepartment, "POST", "/api/v1/departments", "新建部门，由这台电脑托管，创建人是负责人；不需要打开内网可见，局域网里的人也能看到", ScopeConfig, DepartmentInput{}, store.Department{}, nil},
		{DeleteDepartment, "DELETE", "/api/v1/departments/{id}", "删除这台电脑托管的部门", ScopeConfig, nil, Confirmation{}, nil},
		{DepartmentAct, "POST", "/api/v1/departments/{host}/{id}/{action}", "对部门做一件事：join 申请加入、leave 退出、status 查身份、requests 看加入申请（负责人）、decide 同意或拒绝（负责人）、role 设负责人（负责人）、remove 移出成员（负责人）、edit 改名称/头像/上级（负责人）；对其他电脑托管的部门，请求用本机账户密钥签名", ScopeConfig, DepartmentActionInput{}, map[string]any{}, nil},
		{ListModelShares, "GET", "/api/v1/assistant/shares", "可以共享波西米亚模型的同事（已获准查看我面板的人）和是否已共享", ScopeRead, nil, []ModelShareItem{}, nil},
		{SetModelShare, "PUT", "/api/v1/assistant/shares/{id}", "把波西米亚的模型共享给一位同事或收回：对方的请求经这台电脑转发，密钥不离开这台电脑", ScopeConfig, ModelShareInput{}, ModelShareItem{}, nil},
		{UpdateProfile, "PUT", "/api/v1/profile", "修改昵称、上传头像，或随机换一个内置头像", ScopeConfig, ProfileInput{}, store.Profile{}, nil},
		{GetLANKey, "GET", "/api/v1/lan/access-key", "查看内网完全访问密钥状态", ScopeRead, nil, LANKeyStatus{}, nil},
		{RevealLANKey, "POST", "/api/v1/lan/access-key/reveal", "在本机查看现用完全访问密钥", ScopeAdmin, nil, LANKeyResult{}, nil},
		{ChangeLANKeyExpiry, "PUT", "/api/v1/lan/access-key", "在本机修改现用密钥的有效期", ScopeAdmin, LANKeyInput{}, Confirmation{}, nil},
		{GenerateLANKey, "POST", "/api/v1/lan/access-key", "在本机生成或更换完全访问密钥，原密钥立即失效", ScopeAdmin, LANKeyInput{}, LANKeyResult{}, nil},
		{RevokeLANKey, "DELETE", "/api/v1/lan/access-key", "在本机撤销内网密钥和已有的完全访问会话", ScopeAdmin, nil, Confirmation{}, nil},
		{UnlockLAN, "POST", "/api/v1/lan/unlock", "内网访客用密钥解锁当前浏览器", ScopeRead, LANUnlockInput{}, SessionResult{}, nil},
		{ListLANVisitors, "GET", "/api/v1/lan/visitors", "当前通过内网浏览器访问本看板的访客列表", ScopeRead, nil, []LANVisitor{}, nil},
	}
}
