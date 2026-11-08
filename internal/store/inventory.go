package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func appendInventory(ctx context.Context, tx *sql.Tx, row record, receivedAt int64) error {
	var snapshot collector.InventorySnapshot
	if err := json.Unmarshal([]byte(row.detail), &snapshot); err != nil {
		return fmt.Errorf("解析清单快照失败：%w", err)
	}
	key := []any{row.collector, snapshot.Kind, snapshot.Scope}
	var previousSeq, initialized int64
	var epoch string
	err := tx.QueryRowContext(ctx, `SELECT seq,initialized,epoch FROM inventory_scopes WHERE collector_id=? AND kind=? AND scope=?`, key...).Scan(&previousSeq, &initialized, &epoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取清单基线失败：%w", err)
	}
	if row.seq <= previousSeq {
		return nil
	}
	if snapshot.ErrorCode != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO inventory_scopes(collector_id,kind,scope,seq,ts,error_code) VALUES(?,?,?,?,?,?) ON CONFLICT(collector_id,kind,scope) DO UPDATE SET seq=excluded.seq,ts=excluded.ts,error_code=excluded.error_code`, row.collector, snapshot.Kind, snapshot.Scope, row.seq, row.ts, snapshot.ErrorCode)
		if err != nil {
			return fmt.Errorf("记录清单来源错误失败：%w", err)
		}
		return nil
	}
	historyCutoff := func(table string) (int64, error) {
		var cutoff int64
		err := tx.QueryRowContext(ctx, `SELECT raw_complete_from FROM raw_watermarks WHERE table_name=?`, table).Scan(&cutoff)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return cutoff, err
	}
	if snapshot.Kind == "startup" && initialized == 1 && epoch == snapshot.Epoch {
		old := map[string]collector.InventoryEntry{}
		rows, err := tx.QueryContext(ctx, `SELECT id,detail FROM inventory_current WHERE collector_id=? AND kind=? AND scope=?`, key...)
		if err != nil {
			return fmt.Errorf("读取启动项旧状态失败：%w", err)
		}
		for rows.Next() {
			var id, detail string
			if err := rows.Scan(&id, &detail); err != nil {
				rows.Close()
				return err
			}
			var entry collector.InventoryEntry
			if err := json.Unmarshal([]byte(detail), &entry); err != nil {
				rows.Close()
				return err
			}
			old[id] = entry
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		cutoff, err := historyCutoff("inventory_changes")
		if err != nil {
			return fmt.Errorf("读取启动项变化保留水位失败：%w", err)
		}
		change := func(entry collector.InventoryEntry, kind, before, after string) error {
			if row.ts < cutoff {
				return nil
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO inventory_changes(collector_id,kind,scope,seq,ts,id,name,change,old_fingerprint,new_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, row.collector, snapshot.Kind, snapshot.Scope, row.seq, row.ts, strings.ToLower(entry.ID), entry.Name, kind, before, after)
			return err
		}
		for _, entry := range snapshot.Entries {
			id := strings.ToLower(entry.ID)
			previous, exists := old[id]
			if !exists {
				err = change(entry, "added", "", entry.Fingerprint)
			} else if previous.Fingerprint != entry.Fingerprint {
				err = change(entry, "changed", previous.Fingerprint, entry.Fingerprint)
			}
			if err != nil {
				return fmt.Errorf("写入启动项变化失败：%w", err)
			}
			delete(old, id)
		}
		for _, entry := range old {
			if err := change(entry, "removed", entry.Fingerprint, ""); err != nil {
				return fmt.Errorf("写入启动项删除变化失败：%w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM inventory_current WHERE collector_id=? AND kind=? AND scope=?`, key...); err != nil {
		return fmt.Errorf("替换清单当前值失败：%w", err)
	}
	for _, entry := range snapshot.Entries {
		detail, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("编码清单当前值失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_current(collector_id,kind,scope,id,name,ts,detail) VALUES(?,?,?,?,?,?,?)`, row.collector, snapshot.Kind, snapshot.Scope, strings.ToLower(entry.ID), entry.Name, row.ts, string(detail)); err != nil {
			return fmt.Errorf("写入清单当前值失败：%w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_scopes(collector_id,kind,scope,seq,ts,last_success,epoch,error_code,initialized) VALUES(?,?,?,?,?,?,?,'',1) ON CONFLICT(collector_id,kind,scope) DO UPDATE SET seq=excluded.seq,ts=excluded.ts,last_success=excluded.last_success,epoch=excluded.epoch,error_code='',initialized=1`, row.collector, snapshot.Kind, snapshot.Scope, row.seq, row.ts, row.ts, snapshot.Epoch); err != nil {
		return fmt.Errorf("更新清单基线失败：%w", err)
	}
	cutoff, err := historyCutoff("inventory_snapshots")
	if err != nil {
		return fmt.Errorf("读取清单历史保留水位失败：%w", err)
	}
	if row.ts >= cutoff {
		entries := snapshot.Entries
		if entries == nil {
			entries = []collector.InventoryEntry{}
		}
		data, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_snapshots(collector_id,kind,scope,seq,ts,entries) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, row.collector, snapshot.Kind, snapshot.Scope, row.seq, row.ts, string(data)); err != nil {
			return fmt.Errorf("写入清单历史失败：%w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO collector_events(collector_id,seq,ts,kind,message,dropped,source_table,gap_from,gap_to) VALUES(?,?,?,'late_dropped',?,1,'inventory_snapshots',0,0) ON CONFLICT(collector_id,seq) DO NOTHING`, row.collector, row.seq, receivedAt, "清单历史早于原始数据保留水位，当前清单已更新，历史不再补写"); err != nil {
			return fmt.Errorf("记录迟到清单历史缺口失败：%w", err)
		}
	}
	return nil
}
