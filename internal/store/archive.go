// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	archiveFormatVersion = 1
	archiveDefaultLimit  = 500
	archiveMaxLimit      = 5000
	archiveMaxDayChunks  = 50000
)

// ArchiveRecord preserves one expired online database row losslessly in compressed archives.
type ArchiveRecord struct {
	Table       string         `json:"table"`
	TS          int64          `json:"ts"`
	CollectorID string         `json:"collectorId,omitempty"`
	TargetID    string         `json:"targetId,omitempty"`
	Row         map[string]any `json:"row"`
}

// ArchiveChunkMeta describes one verified gzip-compressed archive chunk and its checksums.
type ArchiveChunkMeta struct {
	ID                string         `json:"id"`
	Version           int            `json:"version"`
	Tier              string         `json:"tier"`
	Day               string         `json:"day"`
	File              string         `json:"file"`
	CreatedAt         int64          `json:"createdAt"`
	MinTS             int64          `json:"minTs"`
	MaxTS             int64          `json:"maxTs"`
	RecordCount       int            `json:"recordCount"`
	Tables            map[string]int `json:"tables"`
	UncompressedBytes int64          `json:"uncompressedBytes"`
	CompressedBytes   int64          `json:"compressedBytes"`
	SHA256            string         `json:"sha256"`
	RawSHA256         string         `json:"rawSha256"`
	Verified          bool           `json:"verified"`
}

// ArchiveSummary aggregates totals across all verified archive chunks.
type ArchiveSummary struct {
	ArchiveDir        string         `json:"archiveDir"`
	ChunkCount        int            `json:"chunkCount"`
	TotalRecords      int            `json:"totalRecords"`
	CompressedBytes   int64          `json:"compressedBytes"`
	UncompressedBytes int64          `json:"uncompressedBytes"`
	MinTS             int64          `json:"minTs,omitempty"`
	MaxTS             int64          `json:"maxTs,omitempty"`
	Tables            map[string]int `json:"tables"`
}

// ArchiveExtractQuery filters archived records by time range, table, collector, and target.
type ArchiveExtractQuery struct {
	From        int64    `json:"from"`
	To          int64    `json:"to"`
	Tables      []string `json:"tables,omitempty"`
	CollectorID string   `json:"collectorId,omitempty"`
	TargetID    string   `json:"targetId,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Offset      int      `json:"offset,omitempty"`
}

// ArchiveExtractResult returns extracted archived records with pagination metadata.
type ArchiveExtractResult struct {
	From          int64           `json:"from"`
	To            int64           `json:"to"`
	Total         int             `json:"total"`
	Returned      int             `json:"returned"`
	Offset        int             `json:"offset"`
	Limit         int             `json:"limit"`
	Truncated     bool            `json:"truncated"`
	ChunksScanned int             `json:"chunksScanned"`
	Records       []ArchiveRecord `json:"records"`
}

// TableStorageStat reports row count and estimated payload bytes for one table in lgmon.db.
type TableStorageStat struct {
	Name         string `json:"name"`
	Rows         int64  `json:"rows"`
	PayloadBytes int64  `json:"payloadBytes"`
}

// DBStorageStats summarizes SQLite page usage and per-table footprint.
type DBStorageStats struct {
	PageCount     int64              `json:"pageCount"`
	FreelistCount int64              `json:"freelistCount"`
	PageSize      int64              `json:"pageSize"`
	FreelistBytes int64              `json:"freelistBytes"`
	Tables        []TableStorageStat `json:"tables"`
}

// ArchivableTables lists the 9 historical tables preserved by compressed archiving.
var ArchivableTables = []string{
	"target_status",
	"probe_results",
	"eventlog_events",
	"service_status",
	"task_status",
	"inventory_changes",
	"collector_events",
	"metric_minute",
	"probe_minute",
}

func IsArchivableTable(name string) bool {
	return slices.Contains(ArchivableTables, name)
}

func (s *Store) ArchiveDir() string {
	return s.archiveDir
}

func (v *View) ArchiveDir() string {
	return filepath.Join(filepath.Dir(v.path), "archives")
}

// archiveExpiredForTable archives rows from one table before cleanExpired deletes them,
// returning the maximum rowid safely archived (and -1 if nothing matched).
// It enforces Archive -> Verify -> Commit before returning so callers never delete unarchived rows.
func (s *Store) archiveExpiredForTable(ctx context.Context, tier, table, whereClause string, args []any, nowMS int64) (maxRowID int64, archived int, err error) {
	if s.disableArchive || s.archiveDir == "" {
		return -1, 0, nil
	}
	tx, err := s.maintenanceView.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return -1, 0, fmt.Errorf("打开归档只读快照失败：%w", err)
	}
	defer tx.Rollback()

	records, maxID, err := readArchivableRows(ctx, tx, table, whereClause, args)
	if err != nil {
		return -1, 0, err
	}
	if len(records) == 0 {
		return -1, 0, nil
	}
	chunks, err := writeVerifiedArchiveChunks(s.archiveDir, tier, records, nowMS)
	if err != nil {
		return -1, 0, err
	}
	if len(chunks) == 0 {
		return -1, 0, nil
	}
	s.rollupMu.Lock()
	s.lastArchiveAt = nowMS
	s.lastArchivedRecords += len(records)
	s.rollupMu.Unlock()
	return maxID, len(records), nil
}

func readArchivableRows(ctx context.Context, tx *sql.Tx, table, whereClause string, args []any) ([]ArchiveRecord, int64, error) {
	var records []ArchiveRecord
	var maxRowID int64

	switch table {
	case "target_status":
		rows, err := tx.QueryContext(ctx, `SELECT rowid,collector_id,target_id,ts,state,detail FROM target_status WHERE `+whereClause+` ORDER BY ts,rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 target_status 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, ts int64
			var collectorID, targetID, state, detail string
			if err := rows.Scan(&rowid, &collectorID, &targetID, &ts, &state, &detail); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID, TargetID: targetID,
				Row: map[string]any{
					"collector_id": collectorID, "target_id": targetID, "ts": ts,
					"state": state, "detail": detail,
				},
			})
		}
		return records, maxRowID, rows.Err()

	case "probe_results":
		rows, err := tx.QueryContext(ctx, `SELECT rowid,collector_id,target_id,probe,ts,ok,latency_ms,coalesce(error,''),coalesce(type,''),coalesce(message,''),tls_not_after,coalesce(tls_subject,'') FROM probe_results WHERE `+whereClause+` ORDER BY ts,rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 probe_results 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, ts, okVal, latency int64
			var collectorID, targetID, probe, errMsg, pType, msg, tlsSubj string
			var tlsNotAfter sql.NullInt64
			if err := rows.Scan(&rowid, &collectorID, &targetID, &probe, &ts, &okVal, &latency, &errMsg, &pType, &msg, &tlsNotAfter, &tlsSubj); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			rowMap := map[string]any{
				"collector_id": collectorID, "target_id": targetID, "probe": probe,
				"ts": ts, "ok": okVal, "latency_ms": latency, "error": errMsg,
				"type": pType, "message": msg, "tls_subject": tlsSubj,
			}
			if tlsNotAfter.Valid {
				rowMap["tls_not_after"] = tlsNotAfter.Int64
			} else {
				rowMap["tls_not_after"] = nil
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID, TargetID: targetID, Row: rowMap,
			})
		}
		return records, maxRowID, rows.Err()

	case "eventlog_events":
		rows, err := tx.QueryContext(ctx, `SELECT rowid,collector_id,channel,record_id,ts,provider,event_id,level,subject,message FROM eventlog_events WHERE `+whereClause+` ORDER BY ts,rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 eventlog_events 失败：%w", err)
		}
		type evKey struct {
			collector, channel string
			recordID, ts       int64
		}
		keys := make([]evKey, 0)
		for rows.Next() {
			var rowid, recordID, ts, eventID, level int64
			var collectorID, channel, provider, subject, message string
			if err := rows.Scan(&rowid, &collectorID, &channel, &recordID, &ts, &provider, &eventID, &level, &subject, &message); err != nil {
				rows.Close()
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			keys = append(keys, evKey{collectorID, channel, recordID, ts})
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID,
				Row: map[string]any{
					"collector_id": collectorID, "channel": channel, "record_id": recordID,
					"ts": ts, "provider": provider, "event_id": eventID, "level": level,
					"subject": subject, "message": message,
				},
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, 0, err
		}
		rows.Close()
		for i, k := range keys {
			mRows, mErr := tx.QueryContext(ctx, `SELECT kind,name FROM eventlog_matches WHERE collector_id=? AND channel=? AND record_id=? AND ts=? ORDER BY kind,name`, k.collector, k.channel, k.recordID, k.ts)
			if mErr != nil {
				return nil, 0, fmt.Errorf("读取待归档 eventlog_matches 失败：%w", mErr)
			}
			var matches []map[string]string
			for mRows.Next() {
				var kind, name string
				if err := mRows.Scan(&kind, &name); err != nil {
					mRows.Close()
					return nil, 0, err
				}
				matches = append(matches, map[string]string{"kind": kind, "name": name})
			}
			if err := mRows.Err(); err != nil {
				mRows.Close()
				return nil, 0, err
			}
			mRows.Close()
			if len(matches) > 0 {
				records[i].Row["matches"] = matches
			}
		}
		return records, maxRowID, nil

	case "service_status":
		rows, err := tx.QueryContext(ctx, `SELECT s.rowid,s.collector_id,s.name,s.ts,s.state,s.start_type,s.pid,s.detail FROM service_status s WHERE `+whereClause+` ORDER BY s.ts,s.rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 service_status 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, ts, pid int64
			var collectorID, name, state, startType, detail string
			if err := rows.Scan(&rowid, &collectorID, &name, &ts, &state, &startType, &pid, &detail); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID, TargetID: name,
				Row: map[string]any{
					"collector_id": collectorID, "name": name, "ts": ts,
					"state": state, "start_type": startType, "pid": pid, "detail": detail,
				},
			})
		}
		return records, maxRowID, rows.Err()

	case "task_status":
		rows, err := tx.QueryContext(ctx, `SELECT rowid,collector_id,target_id,ts,state,detail FROM task_status WHERE `+whereClause+` ORDER BY ts,rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 task_status 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, ts int64
			var collectorID, targetID, state, detail string
			if err := rows.Scan(&rowid, &collectorID, &targetID, &ts, &state, &detail); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID, TargetID: targetID,
				Row: map[string]any{
					"collector_id": collectorID, "target_id": targetID, "ts": ts,
					"state": state, "detail": detail,
				},
			})
		}
		return records, maxRowID, rows.Err()

	case "inventory_changes":
		rows, err := tx.QueryContext(ctx, `SELECT rowid,collector_id,kind,scope,seq,ts,id,name,change,old_fingerprint,new_fingerprint FROM inventory_changes WHERE `+whereClause+` ORDER BY ts,rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 inventory_changes 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, seq, ts int64
			var collectorID, kind, scope, id, name, change, oldFP, newFP string
			if err := rows.Scan(&rowid, &collectorID, &kind, &scope, &seq, &ts, &id, &name, &change, &oldFP, &newFP); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID, TargetID: id,
				Row: map[string]any{
					"collector_id": collectorID, "kind": kind, "scope": scope, "seq": seq,
					"ts": ts, "id": id, "name": name, "change": change,
					"old_fingerprint": oldFP, "new_fingerprint": newFP,
				},
			})
		}
		return records, maxRowID, rows.Err()

	case "collector_events":
		rows, err := tx.QueryContext(ctx, `SELECT rowid,collector_id,seq,ts,kind,message,gap_from,gap_to,dropped,source_table,first_ts,last_ts FROM collector_events WHERE `+whereClause+` ORDER BY ts,rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 collector_events 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, seq, ts, gapFrom, gapTo, dropped, firstTS, lastTS int64
			var collectorID, kind, message, sourceTable string
			if err := rows.Scan(&rowid, &collectorID, &seq, &ts, &kind, &message, &gapFrom, &gapTo, &dropped, &sourceTable, &firstTS, &lastTS); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID,
				Row: map[string]any{
					"collector_id": collectorID, "seq": seq, "ts": ts, "kind": kind,
					"message": message, "gap_from": gapFrom, "gap_to": gapTo,
					"dropped": dropped, "source_table": sourceTable,
					"first_ts": firstTS, "last_ts": lastTS,
				},
			})
		}
		return records, maxRowID, rows.Err()

	case "metric_minute":
		rows, err := tx.QueryContext(ctx, `SELECT r.rowid,r.source_table,r.collector_id,r.target_id,r.name,r.resource,r.probe,r.ts,r.samples,r.sum_value,r.max_value FROM metric_minute r WHERE `+whereClause+` ORDER BY r.ts,r.rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 metric_minute 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, ts, samples int64
			var sourceTable, collectorID, targetID, name, resource, probe string
			var sumVal, maxVal float64
			if err := rows.Scan(&rowid, &sourceTable, &collectorID, &targetID, &name, &resource, &probe, &ts, &samples, &sumVal, &maxVal); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID, TargetID: targetID,
				Row: map[string]any{
					"source_table": sourceTable, "collector_id": collectorID, "target_id": targetID,
					"name": name, "resource": resource, "probe": probe, "ts": ts,
					"samples": samples, "sum_value": sumVal, "max_value": maxVal,
				},
			})
		}
		return records, maxRowID, rows.Err()

	case "probe_minute":
		rows, err := tx.QueryContext(ctx, `SELECT r.rowid,r.collector_id,r.target_id,r.probe,r.ts,r.samples,r.successes,r.latency_sum FROM probe_minute r WHERE `+whereClause+` ORDER BY r.ts,r.rowid`, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("读取待归档 probe_minute 失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rowid, ts, samples, successes int64
			var collectorID, targetID, probe string
			var latencySum float64
			if err := rows.Scan(&rowid, &collectorID, &targetID, &probe, &ts, &samples, &successes, &latencySum); err != nil {
				return nil, 0, err
			}
			if rowid > maxRowID {
				maxRowID = rowid
			}
			records = append(records, ArchiveRecord{
				Table: table, TS: ts, CollectorID: collectorID, TargetID: targetID,
				Row: map[string]any{
					"collector_id": collectorID, "target_id": targetID, "probe": probe,
					"ts": ts, "samples": samples, "successes": successes, "latency_sum": latencySum,
				},
			})
		}
		return records, maxRowID, rows.Err()

	default:
		return nil, 0, fmt.Errorf("不支持归档的表 %s", table)
	}
}

func archiveDayKey(ts int64) string {
	return time.UnixMilli(ts).UTC().Format("2006-01-02")
}

func writeVerifiedArchiveChunks(archiveDir, tier string, records []ArchiveRecord, nowMS int64) ([]ArchiveChunkMeta, error) {
	byDay := make(map[string][]ArchiveRecord)
	for _, rec := range records {
		day := archiveDayKey(rec.TS)
		byDay[day] = append(byDay[day], rec)
	}
	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	slices.Sort(days)

	committed := make([]ArchiveChunkMeta, 0, len(days))
	for _, day := range days {
		dayRecords := byDay[day]
		meta, err := writeVerifiedDayChunk(archiveDir, tier, day, dayRecords, nowMS)
		if err != nil {
			return nil, err
		}
		committed = append(committed, meta)
	}
	return committed, nil
}

func writeVerifiedDayChunk(archiveDir, tier, day string, records []ArchiveRecord, nowMS int64) (ArchiveChunkMeta, error) {
	dayDir := filepath.Join(archiveDir, day)
	if err := os.MkdirAll(dayDir, 0o700); err != nil {
		return ArchiveChunkMeta{}, fmt.Errorf("创建归档目录失败：%w", err)
	}
	var minTS, maxTS int64
	tables := make(map[string]int)
	var rawBuf bytes.Buffer
	for i, rec := range records {
		if i == 0 || rec.TS < minTS {
			minTS = rec.TS
		}
		if i == 0 || rec.TS > maxTS {
			maxTS = rec.TS
		}
		tables[rec.Table]++
		line, err := json.Marshal(rec)
		if err != nil {
			return ArchiveChunkMeta{}, fmt.Errorf("编码归档记录失败：%w", err)
		}
		rawBuf.Write(line)
		rawBuf.WriteByte('\n')
	}
	rawBytes := rawBuf.Bytes()
	rawSum := sha256.Sum256(rawBytes)
	rawHex := hex.EncodeToString(rawSum[:])
	shortHash := rawHex[:12]
	chunkID := fmt.Sprintf("%s-%d-%d-%s", tier, minTS, maxTS, shortHash)
	fileName := chunkID + ".jsonl.gz"
	gzPath := filepath.Join(dayDir, fileName)
	metaPath := filepath.Join(dayDir, chunkID+".meta.json")

	// Idempotency: reuse an existing verified chunk with identical raw content digest.
	if existing, err := readChunkMeta(metaPath); err == nil && existing.RawSHA256 == rawHex && existing.RecordCount == len(records) {
		if verifyErr := verifyArchiveFile(gzPath, existing); verifyErr == nil {
			return existing, nil
		}
	}

	tmpGZ := gzPath + ".tmp"
	_ = os.Remove(tmpGZ)
	f, err := os.OpenFile(tmpGZ, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return ArchiveChunkMeta{}, fmt.Errorf("创建归档临时文件失败：%w", err)
	}
	gzHash := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, gzHash)}
	zw, err := gzip.NewWriterLevel(cw, gzip.BestCompression)
	if err != nil {
		f.Close()
		_ = os.Remove(tmpGZ)
		return ArchiveChunkMeta{}, err
	}
	if _, err := zw.Write(rawBytes); err != nil {
		zw.Close()
		f.Close()
		_ = os.Remove(tmpGZ)
		return ArchiveChunkMeta{}, fmt.Errorf("压缩归档数据失败：%w", err)
	}
	if err := zw.Close(); err != nil {
		f.Close()
		_ = os.Remove(tmpGZ)
		return ArchiveChunkMeta{}, fmt.Errorf("完成归档压缩失败：%w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmpGZ)
		return ArchiveChunkMeta{}, fmt.Errorf("落盘归档文件失败：%w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpGZ)
		return ArchiveChunkMeta{}, fmt.Errorf("关闭归档文件失败：%w", err)
	}

	meta := ArchiveChunkMeta{
		ID:                chunkID,
		Version:           archiveFormatVersion,
		Tier:              tier,
		Day:               day,
		File:              filepath.ToSlash(filepath.Join(day, fileName)),
		CreatedAt:         nowMS,
		MinTS:             minTS,
		MaxTS:             maxTS,
		RecordCount:       len(records),
		Tables:            tables,
		UncompressedBytes: int64(len(rawBytes)),
		CompressedBytes:   cw.n,
		SHA256:            hex.EncodeToString(gzHash.Sum(nil)),
		RawSHA256:         rawHex,
		Verified:          true,
	}

	// Mandatory integrity & round-trip verification before atomic commit and online row deletion.
	if err := verifyArchiveFile(tmpGZ, meta); err != nil {
		_ = os.Remove(tmpGZ)
		return ArchiveChunkMeta{}, fmt.Errorf("归档写入校验失败（已保留在线数据）：%w", err)
	}

	if err := os.Rename(tmpGZ, gzPath); err != nil {
		_ = os.Remove(tmpGZ)
		return ArchiveChunkMeta{}, fmt.Errorf("提交归档数据文件失败：%w", err)
	}
	if err := writeMetaAtomically(metaPath, meta); err != nil {
		_ = os.Remove(gzPath)
		return ArchiveChunkMeta{}, err
	}
	return meta, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func writeMetaAtomically(metaPath string, meta ArchiveChunkMeta) error {
	payload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("编码归档元数据失败：%w", err)
	}
	tmp := metaPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("写入归档元数据失败：%w", err)
	}
	if _, err := f.Write(append(payload, '\n')); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("写入归档元数据失败：%w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("落盘归档元数据失败：%w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("关闭归档元数据失败：%w", err)
	}
	if err := os.Rename(tmp, metaPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("提交归档元数据失败：%w", err)
	}
	return nil
}

func verifyArchiveFile(gzPath string, meta ArchiveChunkMeta) error {
	f, err := os.Open(gzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzHash := sha256.New()
	zr, err := gzip.NewReader(io.TeeReader(f, gzHash))
	if err != nil {
		return fmt.Errorf("读取 gzip 流失败：%w", err)
	}
	defer zr.Close()

	rawHash := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(zr, rawHash))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	count := 0
	tables := make(map[string]int)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec ArchiveRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("解析第 %d 条归档记录失败：%w", count+1, err)
		}
		if rec.Table == "" {
			return fmt.Errorf("第 %d 条归档记录缺少表名", count+1)
		}
		tables[rec.Table]++
		count++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("扫描归档流失败：%w", err)
	}
	if err := zr.Close(); err != nil {
		return fmt.Errorf("校验 gzip 尾帧失败：%w", err)
	}
	// Drain any trailing bytes so gzHash reflects the entire file.
	if _, err := io.Copy(gzHash, f); err != nil {
		return err
	}
	if gotGZ := hex.EncodeToString(gzHash.Sum(nil)); meta.SHA256 != "" && gotGZ != meta.SHA256 {
		return fmt.Errorf("归档压缩校验和不匹配")
	}
	if gotRaw := hex.EncodeToString(rawHash.Sum(nil)); meta.RawSHA256 != "" && gotRaw != meta.RawSHA256 {
		return fmt.Errorf("归档原始校验和不匹配")
	}
	if count != meta.RecordCount {
		return fmt.Errorf("归档记录数不匹配（期望 %d，实际 %d）", meta.RecordCount, count)
	}
	for table, want := range meta.Tables {
		if tables[table] != want {
			return fmt.Errorf("表 %s 归档记录数不匹配（期望 %d，实际 %d）", table, want, tables[table])
		}
	}
	return nil
}

func readChunkMeta(metaPath string) (ArchiveChunkMeta, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ArchiveChunkMeta{}, err
	}
	var meta ArchiveChunkMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return ArchiveChunkMeta{}, err
	}
	return meta, nil
}

// VerifyArchiveChunk verifies the compressed checksum, uncompressed checksum, and JSONL record counts of a chunk.
func (v *View) VerifyArchiveChunk(ctx context.Context, chunkID string) error {
	return verifyArchiveChunkInDir(ctx, v.ArchiveDir(), chunkID)
}

func verifyArchiveChunkInDir(ctx context.Context, archiveDir, chunkID string) error {
	if chunkID == "" {
		return fmt.Errorf("归档块 ID 不能为空")
	}
	_, chunks, err := listArchivesInDir(ctx, archiveDir, 0, 0, "")
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if chunk.ID == chunkID {
			return verifyArchiveFile(filepath.Join(archiveDir, filepath.FromSlash(chunk.File)), chunk)
		}
	}
	return fmt.Errorf("未找到归档块 %s", chunkID)
}

func (s *Store) cleanExpiredArchives(ctx context.Context, archiveTTL time.Duration, nowMS int64) error {
	if s.disableArchive || s.archiveDir == "" || archiveTTL <= 0 {
		return nil
	}
	cutoff := nowMS - archiveTTL.Milliseconds()
	entries, err := os.ReadDir(s.archiveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, dayEntry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !dayEntry.IsDir() {
			continue
		}
		dayDir := filepath.Join(s.archiveDir, dayEntry.Name())
		files, err := os.ReadDir(dayDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".meta.json") {
				continue
			}
			metaPath := filepath.Join(dayDir, f.Name())
			meta, err := readChunkMeta(metaPath)
			if err != nil {
				continue
			}
			if meta.MaxTS < cutoff {
				gzPath := filepath.Join(s.archiveDir, filepath.FromSlash(meta.File))
				_ = os.Remove(gzPath)
				_ = os.Remove(metaPath)
			}
		}
		if remaining, err := os.ReadDir(dayDir); err == nil && len(remaining) == 0 {
			_ = os.Remove(dayDir)
		}
	}
	return nil
}

// ListArchives lists verified archive metadata and overall archive summary, optionally filtered.
func (v *View) ListArchives(ctx context.Context, from, to int64, table string) (ArchiveSummary, []ArchiveChunkMeta, error) {
	return listArchivesInDir(ctx, v.ArchiveDir(), from, to, table)
}

func listArchivesInDir(ctx context.Context, archiveDir string, from, to int64, table string) (ArchiveSummary, []ArchiveChunkMeta, error) {
	summary := ArchiveSummary{
		ArchiveDir: archiveDir,
		Tables:     map[string]int{},
	}
	chunks := []ArchiveChunkMeta{}
	if archiveDir == "" {
		return summary, chunks, nil
	}
	if _, err := os.Stat(archiveDir); os.IsNotExist(err) {
		return summary, chunks, nil
	}
	err := filepath.WalkDir(archiveDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			return nil
		}
		meta, err := readChunkMeta(path)
		if err != nil {
			return nil
		}
		if from > 0 && meta.MaxTS < from {
			return nil
		}
		if to > 0 && meta.MinTS > to {
			return nil
		}
		if table != "" && meta.Tables[table] == 0 {
			return nil
		}
		chunks = append(chunks, meta)
		summary.ChunkCount++
		summary.TotalRecords += meta.RecordCount
		summary.CompressedBytes += meta.CompressedBytes
		summary.UncompressedBytes += meta.UncompressedBytes
		if summary.MinTS == 0 || meta.MinTS < summary.MinTS {
			summary.MinTS = meta.MinTS
		}
		if meta.MaxTS > summary.MaxTS {
			summary.MaxTS = meta.MaxTS
		}
		for t, cnt := range meta.Tables {
			summary.Tables[t] += cnt
		}
		return nil
	})
	if err != nil {
		return summary, nil, fmt.Errorf("扫描归档索引失败：%w", err)
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].MinTS != chunks[j].MinTS {
			return chunks[i].MinTS > chunks[j].MinTS
		}
		return chunks[i].ID > chunks[j].ID
	})
	return summary, chunks, nil
}

// ExtractArchive extracts archived records matching the time range, tables, collector, and target.
func (v *View) ExtractArchive(ctx context.Context, q ArchiveExtractQuery) (ArchiveExtractResult, error) {
	return extractArchiveFromDir(ctx, v.ArchiveDir(), q)
}

func extractArchiveFromDir(ctx context.Context, archiveDir string, q ArchiveExtractQuery) (ArchiveExtractResult, error) {
	if q.From < 0 || (q.To > 0 && q.To < q.From) {
		return ArchiveExtractResult{}, fmt.Errorf("提取时间范围无效：from 不能晚于 to")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = archiveDefaultLimit
	}
	if limit > archiveMaxLimit {
		limit = archiveMaxLimit
	}
	offset := max(0, q.Offset)

	tableFilter := make(map[string]bool, len(q.Tables))
	for _, t := range q.Tables {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			continue
		}
		if !IsArchivableTable(trimmed) {
			return ArchiveExtractResult{}, fmt.Errorf("不支持按需提取的表 %q", trimmed)
		}
		tableFilter[trimmed] = true
	}

	_, chunks, err := listArchivesInDir(ctx, archiveDir, q.From, q.To, "")
	if err != nil {
		return ArchiveExtractResult{}, err
	}

	matched := make([]ArchiveRecord, 0)
	seenKeys := make(map[string]struct{})
	scanned := 0

	for _, chunk := range chunks {
		if ctx.Err() != nil {
			return ArchiveExtractResult{}, ctx.Err()
		}
		if len(tableFilter) > 0 {
			hasTable := false
			for t := range tableFilter {
				if chunk.Tables[t] > 0 {
					hasTable = true
					break
				}
			}
			if !hasTable {
				continue
			}
		}
		scanned++
		gzPath := filepath.Join(archiveDir, filepath.FromSlash(chunk.File))
		if err := readArchiveChunkRecords(ctx, gzPath, func(rec ArchiveRecord) error {
			if q.From > 0 && rec.TS < q.From {
				return nil
			}
			if q.To > 0 && rec.TS > q.To {
				return nil
			}
			if len(tableFilter) > 0 && !tableFilter[rec.Table] {
				return nil
			}
			if q.CollectorID != "" && rec.CollectorID != q.CollectorID {
				return nil
			}
			if q.TargetID != "" && rec.TargetID != q.TargetID {
				return nil
			}
			rowJSON, _ := json.Marshal(rec.Row)
			dedupKey := fmt.Sprintf("%s|%d|%s|%s|%s", rec.Table, rec.TS, rec.CollectorID, rec.TargetID, string(rowJSON))
			if _, exists := seenKeys[dedupKey]; exists {
				return nil
			}
			seenKeys[dedupKey] = struct{}{}
			matched = append(matched, rec)
			return nil
		}); err != nil {
			return ArchiveExtractResult{}, err
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].TS != matched[j].TS {
			return matched[i].TS < matched[j].TS
		}
		if matched[i].Table != matched[j].Table {
			return matched[i].Table < matched[j].Table
		}
		if matched[i].CollectorID != matched[j].CollectorID {
			return matched[i].CollectorID < matched[j].CollectorID
		}
		return matched[i].TargetID < matched[j].TargetID
	})

	total := len(matched)
	end := min(total, offset+limit)
	start := min(total, offset)
	page := matched[start:end]
	return ArchiveExtractResult{
		From:          q.From,
		To:            q.To,
		Total:         total,
		Returned:      len(page),
		Offset:        offset,
		Limit:         limit,
		Truncated:     end < total,
		ChunksScanned: scanned,
		Records:       page,
	}, nil
}

func readArchiveChunkRecords(ctx context.Context, gzPath string, visit func(ArchiveRecord) error) error {
	f, err := os.Open(gzPath)
	if err != nil {
		return fmt.Errorf("打开归档文件失败：%w", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("解压归档文件失败：%w", err)
	}
	defer zr.Close()
	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec ArchiveRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("解析归档记录失败：%w", err)
		}
		if err := visit(rec); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// DBStorageStats reports SQLite page statistics and per-table row/payload breakdown.
func (v *View) DBStorageStats(ctx context.Context) (DBStorageStats, error) {
	var stats DBStorageStats
	if err := v.db.QueryRowContext(ctx, `SELECT (SELECT page_count FROM pragma_page_count()), (SELECT freelist_count FROM pragma_freelist_count()), (SELECT page_size FROM pragma_page_size())`).Scan(&stats.PageCount, &stats.FreelistCount, &stats.PageSize); err != nil {
		return stats, fmt.Errorf("读取数据库页统计失败：%w", err)
	}
	stats.FreelistBytes = stats.FreelistCount * stats.PageSize

	tableSpecs := []struct {
		name string
		expr string
	}{
		{"metric_minute", "64"},
		{"metric_hour", "64"},
		{"network_metrics", "48"},
		{"disk_metrics", "48"},
		{"process_samples", "48"},
		{"system_metrics", "48 + COALESCE(LENGTH(detail), 0)"},
		{"target_status", "40 + COALESCE(LENGTH(detail), 0)"},
		{"probe_results", "48 + COALESCE(LENGTH(error), 0) + COALESCE(LENGTH(message), 0) + COALESCE(LENGTH(tls_subject), 0)"},
		{"probe_minute", "48"},
		{"probe_hour", "48"},
		{"inventory_snapshots", "48 + COALESCE(LENGTH(entries), 0)"},
		{"inventory_changes", "96"},
		{"service_status", "48 + COALESCE(LENGTH(detail), 0)"},
		{"task_status", "40 + COALESCE(LENGTH(detail), 0)"},
		{"eventlog_events", "64 + COALESCE(LENGTH(message), 0)"},
		{"collector_events", "48 + COALESCE(LENGTH(message), 0)"},
		{"script_metrics", "48"},
		{"alerts", "64 + COALESCE(LENGTH(summary), 0)"},
		{"job_events", "64 + COALESCE(LENGTH(event), 0)"},
	}
	for _, spec := range tableSpecs {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		var rows, payload int64
		query := fmt.Sprintf(`SELECT COUNT(*), COALESCE(SUM(%s), 0) FROM %s`, spec.expr, spec.name)
		if err := v.db.QueryRowContext(ctx, query).Scan(&rows, &payload); err != nil {
			continue
		}
		if rows == 0 {
			continue
		}
		stats.Tables = append(stats.Tables, TableStorageStat{
			Name:         spec.name,
			Rows:         rows,
			PayloadBytes: payload,
		})
	}
	sort.Slice(stats.Tables, func(i, j int) bool {
		if stats.Tables[i].PayloadBytes != stats.Tables[j].PayloadBytes {
			return stats.Tables[i].PayloadBytes > stats.Tables[j].PayloadBytes
		}
		return stats.Tables[i].Rows > stats.Tables[j].Rows
	})
	if len(stats.Tables) > 10 {
		stats.Tables = stats.Tables[:10]
	}
	return stats, nil
}
