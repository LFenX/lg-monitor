// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

const (
	GetCloudStatus        Operation = "get_cloud_status"
	CloudRequestOTP       Operation = "cloud_request_otp"
	CloudVerifyOTP        Operation = "cloud_verify_otp"
	CloudRegister         Operation = "cloud_register"
	CloudLoginPassword    Operation = "cloud_login_password"
	CloudSetPassword      Operation = "cloud_set_password"
	CloudResetPassword    Operation = "cloud_reset_password"
	CloudLogout           Operation = "cloud_logout"
	UpdateCloudSettings   Operation = "update_cloud_settings"
	TriggerCloudSync      Operation = "trigger_cloud_sync"
	ListCloudDevices      Operation = "list_cloud_devices"
	RenameCloudDevice     Operation = "rename_cloud_device"
	MergeCloudDevice      Operation = "merge_cloud_device"
	UnbindCloudDevice     Operation = "unbind_cloud_device"
	GetCloudDeviceBoard   Operation = "get_cloud_device_board"
	GetCloudDeviceMonitor Operation = "get_cloud_device_monitor"
	GetCloudDeviceDoc     Operation = "get_cloud_device_doc"
	GetCloudDeviceJobDays Operation = "get_cloud_device_job_days"
	GetCloudDeviceJobDay  Operation = "get_cloud_device_job_day"
	ListExternalServices  Operation = "list_external_services"
	GetCloudDeviceSystem  Operation = "get_cloud_device_system"
	ListCloudRelations    Operation = "list_cloud_relations"
)

// RelationEnd is one side of a dependency between services on different devices of the account.
type RelationEnd struct {
	Device       string `json:"device"`
	DeviceName   string `json:"deviceName"`
	DeviceAvatar string `json:"deviceAvatar,omitempty"`
	Current      bool   `json:"current,omitempty" jsonschema_description:"true 表示就是这台电脑。"`
	Online       bool   `json:"online"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state" jsonschema:"enum=up,enum=down,enum=unknown"`
	Issue        string `json:"issue,omitempty"`
}

// CrossDeviceRelation says From depends on To; the two services live on different devices of the account.
type CrossDeviceRelation struct {
	From     RelationEnd `json:"from"`
	To       RelationEnd `json:"to"`
	Relation string      `json:"relation,omitempty"`
}

type CrossDeviceRelations struct {
	Relations []CrossDeviceRelation `json:"relations"`
	Devices   int                   `json:"devices" jsonschema_description:"关系涉及的设备数。"`
	Problems  int                   `json:"problems" jsonschema_description:"有一端异常、离线或找不到的关系数。"`
}

// CloudDeviceSystem is another device's computers as its last synced sample showed them: CPU, memory, swap,
// disks, network, what programs and containers use, and the CPU, memory and swap curves that came with it.
type CloudDeviceSystem struct {
	DeviceID     string   `json:"deviceId"`
	DeviceName   string   `json:"deviceName"`
	DeviceAvatar string   `json:"deviceAvatar,omitempty"`
	DeviceOS     string   `json:"deviceOs,omitempty"`
	Online       bool     `json:"online"`
	LastSeen     int64    `json:"lastSeen"`
	StateUpdated int64    `json:"stateUpdated,omitempty"`
	Devices      []Device `json:"devices" jsonschema_description:"那台设备监控的电脑（通常就是它自己）最近一次同步的采样；旧版本设备没有同步时为空。"`
}

type CloudSyncStats struct {
	BytesSent      int64 `json:"bytesSent"`
	BytesReceived  int64 `json:"bytesReceived"`
	PushCount      int64 `json:"pushCount"`
	HeartbeatCount int64 `json:"heartbeatCount"`
	PullCount      int64 `json:"pullCount"`
	DocUploads     int64 `json:"docUploads"`
}

type CloudStatus struct {
	LoggedIn     bool           `json:"loggedIn"`
	Email        string         `json:"email,omitempty"`
	HasPassword  bool           `json:"hasPassword,omitempty"`
	DeviceID     string         `json:"deviceId"`
	DeviceName   string         `json:"deviceName"`
	OS           string         `json:"os,omitempty"`
	ServerURL    string         `json:"serverUrl"`
	Paused       bool           `json:"paused"`
	Syncing      bool           `json:"syncing"`
	LastPushAt   int64          `json:"lastPushAt,omitempty"`
	LastPullAt   int64          `json:"lastPullAt,omitempty"`
	LastError    string         `json:"lastError,omitempty"`
	DevicesCount int            `json:"devicesCount"`
	Remember     bool           `json:"remember,omitempty" jsonschema_description:"true 表示重启后保持登录。"`
	LastEmail    string         `json:"lastEmail,omitempty" jsonschema_description:"这台电脑上次登录、且仍保留设备记录的账号；再次登录会沿用原来的设备。"`
	Binding      string         `json:"binding,omitempty" jsonschema:"enum=,enum=new,enum=known" jsonschema_description:"只在登录或注册的回复里出现：new 是新增设备，known 是沿用这台电脑原有的设备记录。"`
	Stats        CloudSyncStats `json:"stats"`
}

type CloudOTPRequestInput struct {
	Email   string `json:"email" jsonschema:"minLength=3,maxLength=254"`
	Purpose string `json:"purpose,omitempty" jsonschema:"enum=,enum=login,enum=register,enum=reset"`
	// Lang picks the email's language; the panel sends its own.
	Lang string `json:"lang,omitempty" jsonschema:"enum=,enum=zh,enum=en"`
}

type CloudRegisterInput struct {
	Email      string `json:"email" jsonschema:"minLength=3,maxLength=254"`
	Code       string `json:"code" jsonschema:"minLength=6,maxLength=6"`
	Password   string `json:"password,omitempty" jsonschema:"maxLength=128"`
	DeviceName string `json:"deviceName,omitempty" jsonschema:"maxLength=40"`
	// Remember defaults to true: without it the sign-in ends when lgmon or the computer restarts.
	Remember *bool `json:"remember,omitempty" jsonschema_description:"重启后保持登录，默认 true；false 时电脑或 lgmon 重启后需要重新登录。"`
}

type CloudOTPResult struct {
	OK           bool `json:"ok"`
	CooldownSec  int  `json:"cooldownSec"`
	ExpiresInSec int  `json:"expiresInSec"`
}

type CloudOTPVerifyInput struct {
	Email      string `json:"email" jsonschema:"minLength=3,maxLength=254"`
	Code       string `json:"code" jsonschema:"minLength=6,maxLength=6"`
	DeviceName string `json:"deviceName,omitempty" jsonschema:"maxLength=40"`
	Remember   *bool  `json:"remember,omitempty" jsonschema_description:"重启后保持登录，默认 true；false 时电脑或 lgmon 重启后需要重新登录。"`
}

type CloudPasswordLoginInput struct {
	Email      string `json:"email" jsonschema:"minLength=3,maxLength=254"`
	Password   string `json:"password" jsonschema:"minLength=1,maxLength=128"`
	DeviceName string `json:"deviceName,omitempty" jsonschema:"maxLength=40"`
	Remember   *bool  `json:"remember,omitempty" jsonschema_description:"重启后保持登录，默认 true；false 时电脑或 lgmon 重启后需要重新登录。"`
}

type CloudSetPasswordInput struct {
	CurrentPassword string `json:"currentPassword,omitempty" jsonschema:"maxLength=128"`
	NewPassword     string `json:"newPassword" jsonschema:"minLength=8,maxLength=128"`
}

type CloudResetPasswordInput struct {
	Email       string `json:"email" jsonschema:"minLength=3,maxLength=254"`
	Code        string `json:"code" jsonschema:"minLength=6,maxLength=6"`
	NewPassword string `json:"newPassword" jsonschema:"minLength=8,maxLength=128"`
}

type CloudLogoutInput struct {
	UnbindDevice bool `json:"unbindDevice,omitempty"`
}

type CloudSettingsInput struct {
	ServerURL  *string `json:"serverUrl,omitempty" jsonschema:"maxLength=256"`
	Paused     *bool   `json:"paused,omitempty"`
	DeviceName *string `json:"deviceName,omitempty" jsonschema:"maxLength=40"`
	// DeviceOrder replaces the whole order; an empty list goes back to the default.
	DeviceOrder *[]string `json:"deviceOrder,omitempty" jsonschema:"maxItems=64" jsonschema_description:"账号下设备在本机网页和启动小窗里的显示顺序（设备 ID），整体替换；空列表恢复默认（本机在最前）。"`
}

type CloudRenameDeviceInput struct {
	Name string `json:"name" jsonschema:"minLength=1,maxLength=40"`
}

type CloudDeviceSummary struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Avatar       string      `json:"avatar,omitempty"`
	OS           string      `json:"os,omitempty"`
	Version      string      `json:"version,omitempty"`
	IsCurrent    bool        `json:"isCurrent"`
	SameMachine  bool        `json:"sameMachine,omitempty" jsonschema_description:"true 表示这是本机以前留下的另一条设备记录，可以用 merge_cloud_device 合并。"`
	Online       bool        `json:"online"`
	LastSeen     int64       `json:"lastSeen"`
	StateUpdated int64       `json:"stateUpdated,omitempty"`
	TotalCount   int         `json:"totalCount"`
	DownCount    int         `json:"downCount"`
	Boards       []BoardInfo `json:"boards"`
}

// ExternalCandidate is one service on another computer of the account, as an agent picks a cross-device dependency.
type ExternalCandidate struct {
	Device     string   `json:"device" jsonschema_description:"设备 ID：写进 edit_monitor 的 depends[].device。"`
	DeviceName string   `json:"deviceName"`
	Online     bool     `json:"online"`
	ID         string   `json:"id" jsonschema_description:"服务 ID：写进 depends[].target。"`
	Name       string   `json:"name"`
	Role       string   `json:"role,omitempty"`
	Parent     string   `json:"parent,omitempty"`
	State      string   `json:"state"`
	Boards     []string `json:"boards,omitempty" jsonschema_description:"在那台设备上所在的面板名称。"`
	Watching   []string `json:"watching,omitempty" jsonschema_description:"那台设备监控的对象，帮助判断是不是用户说的那个服务。"`
}

type CloudBoardView struct {
	DeviceID     string    `json:"deviceId"`
	DeviceName   string    `json:"deviceName"`
	DeviceAvatar string    `json:"deviceAvatar,omitempty"`
	DeviceOS     string    `json:"deviceOs,omitempty"`
	Online       bool      `json:"online"`
	LastSeen     int64     `json:"lastSeen"`
	StateUpdated int64     `json:"stateUpdated,omitempty"`
	Board        BoardView `json:"board"`
}

func cloudRoutes() []Route {
	return []Route{
		{GetCloudStatus, "GET", "/api/v1/cloud/status", "读取云账号登录状态、同步配置与累计省流统计", ScopeRead, nil, CloudStatus{}, nil},
		{CloudRequestOTP, "POST", "/api/v1/cloud/auth/otp/request", "向指定邮箱发送 6 位注册、登录或重置密码验证码", ScopeConfig, CloudOTPRequestInput{}, CloudOTPResult{}, nil},
		{CloudRegister, "POST", "/api/v1/cloud/auth/register", "用注册验证码注册新账号（可同时设置密码）并绑定当前设备", ScopeConfig, CloudRegisterInput{}, CloudStatus{}, nil},
		{CloudVerifyOTP, "POST", "/api/v1/cloud/auth/otp/verify", "使用邮箱验证码登录已注册的账号并绑定当前设备（默认登录方式）", ScopeConfig, CloudOTPVerifyInput{}, CloudStatus{}, nil},
		{CloudLoginPassword, "POST", "/api/v1/cloud/auth/login", "使用邮箱和密码登录并绑定当前设备", ScopeConfig, CloudPasswordLoginInput{}, CloudStatus{}, nil},
		{CloudSetPassword, "POST", "/api/v1/cloud/auth/password", "为当前云账号设置或修改登录密码", ScopeConfig, CloudSetPasswordInput{}, CloudStatus{}, nil},
		{CloudResetPassword, "POST", "/api/v1/cloud/auth/password/reset", "使用邮箱验证码重置账号密码", ScopeConfig, CloudResetPasswordInput{}, Confirmation{}, nil},
		{CloudLogout, "POST", "/api/v1/cloud/auth/logout", "退出云账号登录（可选同时解绑并清理本机云端数据）", ScopeConfig, CloudLogoutInput{}, CloudStatus{}, nil},
		{UpdateCloudSettings, "PUT", "/api/v1/cloud/settings", "修改云同步服务器地址、本机设备名、设备显示顺序，或暂停/恢复同步", ScopeConfig, CloudSettingsInput{}, CloudStatus{}, nil},
		{TriggerCloudSync, "POST", "/api/v1/cloud/sync", "立即触发一次增量云同步", ScopeConfig, nil, CloudStatus{}, nil},
		{ListCloudDevices, "GET", "/api/v1/cloud/devices", "读取同一账号下已绑定的所有设备及其面板摘要", ScopeRead, nil, []CloudDeviceSummary{}, nil},
		{RenameCloudDevice, "PUT", "/api/v1/cloud/devices/{deviceId}", "修改账号下指定设备的显示名称", ScopeConfig, CloudRenameDeviceInput{}, Confirmation{}, nil},
		{MergeCloudDevice, "POST", "/api/v1/cloud/devices/{deviceId}/merge", "把本机以前留下的设备记录合并到本机：本机沿用那条记录的设备标识，现在这条重复记录删除；那条记录必须离线", ScopeConfig, nil, Confirmation{}, nil},
		{UnbindCloudDevice, "DELETE", "/api/v1/cloud/devices/{deviceId}", "从账号移除指定设备，并删除它在云端的快照、文档和任务记录", ScopeConfig, nil, Confirmation{}, nil},
		{GetCloudDeviceBoard, "GET", "/api/v1/cloud/devices/{deviceId}/boards/{boardId}", "只读查看云同步设备的面板视图", ScopeRead, nil, CloudBoardView{}, nil},
		{GetCloudDeviceMonitor, "GET", "/api/v1/cloud/devices/{deviceId}/monitors/{monitorId}", "只读查看云同步设备的服务状态详情", ScopeRead, nil, MonitorDetail{}, nil},
		{GetCloudDeviceDoc, "GET", "/api/v1/cloud/devices/{deviceId}/monitors/{monitorId}/doc", "按需拉取云同步设备的服务活文档（带 ETag 缓存）", ScopeRead, nil, MonitorDoc{}, nil},
		{GetCloudDeviceJobDays, "GET", "/api/v1/cloud/devices/{deviceId}/jobs/{monitorId}/days", "按需拉取云同步设备的批处理任务每日记录（带 ETag 缓存）", ScopeRead, nil, JobDays{}, []Parameter{{"days", "最近几天，1～90，默认 14"}}},
		{ListExternalServices, "GET", "/api/v1/cloud/services", "列出同一云账号其他设备上的全部服务，用来补跨设备的外部依赖", ScopeRead, nil, []ExternalCandidate{}, nil},
		{ListCloudRelations, "GET", "/api/v1/cloud/relations", "汇总同一云账号各设备之间的跨设备依赖：谁依赖谁、两端状态；同一关系只出现一次", ScopeRead, nil, CrossDeviceRelations{}, nil},
		{GetCloudDeviceSystem, "GET", "/api/v1/cloud/devices/{deviceId}/system", "只读查看云同步设备最近一次同步的电脑资源：CPU、内存、交换区、磁盘、网卡和程序/容器占用（约 5 分钟更新）", ScopeRead, nil, CloudDeviceSystem{}, nil},
		{GetCloudDeviceJobDay, "GET", "/api/v1/cloud/devices/{deviceId}/jobs/{monitorId}/days/{date}", "按需查看云同步设备批处理任务某天的阶段明细、分区与生产通路现状", ScopeRead, nil, JobDayDetail{}, nil},
	}
}
