package api

import (
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

// LAN sharing: a computer may announce its boards on the local network; reading one needs a
// request, a matching pairing code on both screens, and the owner's approval.
const (
	GetLAN         Operation = "lan_status"
	ConfigureLAN   Operation = "lan_configure"
	DiscoverLAN    Operation = "lan_discover"
	LANDirectory   Operation = "lan_directory"
	RequestLAN     Operation = "lan_request"
	ListLANRequest Operation = "lan_requests"
	ApproveLAN     Operation = "lan_approve"
	DenyLAN        Operation = "lan_deny"
	CancelLAN      Operation = "lan_cancel"
	ListLANGrants  Operation = "lan_grants"
	RevokeLANGrant Operation = "lan_revoke"
	ListLANRemote  Operation = "lan_remote"
	GetLANRemote   Operation = "lan_remote_board"
)

type LANState struct {
	Visible     bool     `json:"visible"`
	Name        string   `json:"name" jsonschema_description:"别人在内网里看到的名字。"`
	Hostname    string   `json:"hostname"`
	Fingerprint string   `json:"fingerprint" jsonschema_description:"本机证书指纹的短格式，对方核对用。"`
	Addresses   []string `json:"addresses" jsonschema_description:"别人手动连接时可以输入的地址。"`
	Listening   bool     `json:"listening" jsonschema_description:"成员端口是否已经在监听。"`
	Problem     string   `json:"problem,omitempty"`
	Firewall    string   `json:"firewall,omitempty" jsonschema:"enum=allowed,enum=blocked" jsonschema_description:"allowed：已在 Windows 防火墙为内网和 Tailscale 地址放行成员端口与发现端口；blocked：没能放行，别人搜不到也连不上。"`
	FirewallFix string   `json:"firewallFix,omitempty" jsonschema_description:"blocked 时在管理员 PowerShell 里运行的放行命令。"`
	Pending     int      `json:"pending" jsonschema_description:"等待你处理的申请数。"`
	Viewers     int      `json:"viewers" jsonschema_description:"正在生效的授权数。"`
}

type LANConfigInput struct {
	Visible *bool   `json:"visible,omitempty"`
	Name    *string `json:"name,omitempty" jsonschema:"maxLength=40"`
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
	ID        string    `json:"id" jsonschema_description:"本机记录的申请 ID，用来读取这个远程面板。"`
	OwnerName string    `json:"ownerName"`
	Address   string    `json:"address"`
	Board     BoardView `json:"board"`
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
	}
}
