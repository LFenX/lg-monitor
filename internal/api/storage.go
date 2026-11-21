// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

const (
	GetStorage     Operation = "get_storage"
	MigrateStorage Operation = "migrate_storage"
)

// DiskVolume describes capacity and a suggested lgmon data path on one local drive.
type DiskVolume struct {
	Root          string `json:"root"`
	TotalBytes    uint64 `json:"totalBytes"`
	FreeBytes     uint64 `json:"freeBytes"`
	UsedBytes     uint64 `json:"usedBytes"`
	SuggestedPath string `json:"suggestedPath,omitempty"`
	Current       bool   `json:"current,omitempty"`
}

// StorageMigrationState reports live progress of a background data directory migration.
type StorageMigrationState struct {
	State        string `json:"state" jsonschema:"enum=idle,enum=preparing,enum=copying,enum=verifying,enum=switching,enum=done,enum=error"`
	Source       string `json:"source,omitempty"`
	Target       string `json:"target,omitempty"`
	Percent      int    `json:"percent"`
	CopiedBytes  int64  `json:"copiedBytes"`
	TotalBytes   int64  `json:"totalBytes"`
	CopiedFiles  int    `json:"copiedFiles"`
	TotalFiles   int    `json:"totalFiles"`
	CurrentFile  string `json:"currentFile,omitempty"`
	StartedAt    int64  `json:"startedAt,omitempty"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
	FinishedAt   int64  `json:"finishedAt,omitempty"`
	NeedsRestart bool   `json:"needsRestart,omitempty"`
	Error        string `json:"error,omitempty"`
}

// StorageStatus summarizes lgmon's active data home, disk usage, available drives, and migration progress.
type StorageStatus struct {
	Home         string                 `json:"home"`
	DefaultHome  string                 `json:"defaultHome"`
	Redirected   bool                   `json:"redirected"`
	DataBytes    int64                  `json:"dataBytes"`
	DBBytes      int64                  `json:"dbBytes"`
	ConfigBytes  int64                  `json:"configBytes"`
	LogsBytes    int64                  `json:"logsBytes"`
	OtherBytes   int64                  `json:"otherBytes"`
	FileCount    int                    `json:"fileCount"`
	CurrentDrive *DiskVolume            `json:"currentDrive,omitempty"`
	Drives       []DiskVolume           `json:"drives"`
	Migration    *StorageMigrationState `json:"migration,omitempty"`
}

// StorageMigrateInput specifies the new local directory to migrate lgmon's data into.
type StorageMigrateInput struct {
	Target string `json:"target" jsonschema:"minLength=1,maxLength=400" jsonschema_description:"新的本机数据目录绝对路径，例如 D:\\lgmon-data。"`
}

func storageRoutes() []Route {
	return []Route{
		{GetStorage, "GET", "/api/v1/storage", "读取当前数据存储目录、占用空间、本地磁盘可用空间与迁移进度", ScopeRead, nil, StorageStatus{}, nil},
		{MigrateStorage, "POST", "/api/v1/storage/migrate", "将 lgmon 数据目录安全迁移至新的本机目录（异步执行，可通过 GET /api/v1/storage 轮询进度）", ScopeConfig, StorageMigrateInput{}, StorageStatus{}, nil},
	}
}
