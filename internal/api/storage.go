// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import "github.com/LFenX/lg-monitor/internal/store"

const (
	GetStorage     Operation = "get_storage"
	MigrateStorage Operation = "migrate_storage"
	CompactStorage Operation = "compact_storage"
	ListArchives   Operation = "list_archives"
	ExtractArchive Operation = "extract_archive"
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

// StorageFileItem summarizes one largest file in the lgmon data directory.
type StorageFileItem struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Category string `json:"category"`
}

// StorageTableItem summarizes one table in lgmon.db.
type StorageTableItem struct {
	Name         string `json:"name"`
	Rows         int64  `json:"rows"`
	PayloadBytes int64  `json:"payloadBytes"`
}

// StorageAutoCleanStatus reports periodic background cleanup status.
type StorageAutoCleanStatus struct {
	LastRunAt      int64 `json:"lastRunAt,omitempty"`
	LastFreedBytes int64 `json:"lastFreedBytes,omitempty"`
	RemovedFiles   int   `json:"removedFiles,omitempty"`
	Runs           int   `json:"runs,omitempty"`
	LastPruneAt    int64 `json:"lastPruneAt,omitempty"`
	LastVacuumAt   int64 `json:"lastVacuumAt,omitempty"`
	LastArchiveAt  int64 `json:"lastArchiveAt,omitempty"`
}

// StorageStatus summarizes lgmon's active data home, disk usage, available drives, and migration progress.
type StorageStatus struct {
	Home          string                  `json:"home"`
	DefaultHome   string                  `json:"defaultHome"`
	Redirected    bool                    `json:"redirected"`
	DataBytes     int64                   `json:"dataBytes"`
	DBBytes       int64                   `json:"dbBytes"`
	WALBytes      int64                   `json:"walBytes,omitempty"`
	FreelistBytes int64                   `json:"freelistBytes,omitempty"`
	ArchiveBytes  int64                   `json:"archiveBytes,omitempty"`
	ConfigBytes   int64                   `json:"configBytes"`
	LogsBytes     int64                   `json:"logsBytes"`
	OtherBytes    int64                   `json:"otherBytes"`
	FileCount     int                     `json:"fileCount"`
	TopFiles      []StorageFileItem       `json:"topFiles,omitempty"`
	Tables        []StorageTableItem      `json:"tables,omitempty"`
	Archives      *store.ArchiveSummary   `json:"archives,omitempty"`
	AutoClean     *StorageAutoCleanStatus `json:"autoClean,omitempty"`
	CurrentDrive  *DiskVolume             `json:"currentDrive,omitempty"`
	Drives        []DiskVolume            `json:"drives"`
	Migration     *StorageMigrationState  `json:"migration,omitempty"`
}

// StorageMigrateInput specifies the new local directory to migrate lgmon's data into.
type StorageMigrateInput struct {
	Target string `json:"target" jsonschema:"minLength=1,maxLength=400" jsonschema_description:"新的本机数据目录绝对路径，例如 D:\\lgmon-data。"`
}

// StorageCompactResult reports how many bytes were reclaimed by compacting databases and logs.
type StorageCompactResult struct {
	BeforeBytes     int64         `json:"beforeBytes"`
	AfterBytes      int64         `json:"afterBytes"`
	FreedBytes      int64         `json:"freedBytes"`
	ArchivedRecords int           `json:"archivedRecords,omitempty"`
	ArchiveBytes    int64         `json:"archiveBytes,omitempty"`
	Storage         StorageStatus `json:"storage"`
}

// ArchiveListResult summarizes verified compressed historical archive chunks.
type ArchiveListResult struct {
	Summary store.ArchiveSummary     `json:"summary"`
	Chunks  []store.ArchiveChunkMeta `json:"chunks"`
}

func storageRoutes() []Route {
	return []Route{
		{GetStorage, "GET", "/api/v1/storage", "读取当前数据存储目录、占用空间、本地磁盘可用空间与迁移进度", ScopeRead, nil, StorageStatus{}, nil},
		{MigrateStorage, "POST", "/api/v1/storage/migrate", "将 lgmon 数据目录安全迁移至新的本机目录（异步执行，可通过 GET /api/v1/storage 轮询进度）", ScopeConfig, StorageMigrateInput{}, StorageStatus{}, nil},
		{CompactStorage, "POST", "/api/v1/storage/compact", "清理过期采样与历史快照、压缩归档历史数据、整理 SQLite 数据库与 WAL 并裁剪超大日志文件", ScopeConfig, nil, StorageCompactResult{}, nil},
		{ListArchives, "GET", "/api/v1/storage/archives", "列出已压缩验证的历史数据归档块及汇总信息", ScopeRead, nil, ArchiveListResult{}, []Parameter{
			{Name: "from", Description: "开始时间，UTC Unix 毫秒（可选）"},
			{Name: "to", Description: "结束时间，UTC Unix 毫秒（可选）"},
			{Name: "table", Description: "按表名筛选（可选）"},
		}},
		{ExtractArchive, "GET", "/api/v1/storage/archives/extract", "按时间段、表名、采集器或目标从历史压缩归档中按需提取原始记录", ScopeRead, nil, store.ArchiveExtractResult{}, []Parameter{
			{Name: "from", Description: "开始时间，UTC Unix 毫秒（可选）"},
			{Name: "to", Description: "结束时间，UTC Unix 毫秒（可选）"},
			{Name: "table", Description: "表名，支持逗号分隔多个表（可选）"},
			{Name: "collector", Description: "采集器 ID（可选）"},
			{Name: "target", Description: "目标或服务 ID（可选）"},
			{Name: "limit", Description: "最多返回条数，默认 500，上限 5000"},
			{Name: "offset", Description: "偏移量，默认 0"},
		}},
	}
}
