// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Profile is this computer's account: one row, created the first time lgmon starts, so a new
// installation already has a face on the network before anyone opens a settings page.
type Profile struct {
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

var ErrNoProfile = errors.New("本机账户还没有创建")

func (s *AuthDB) initProfile(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS profile(id INTEGER PRIMARY KEY CHECK(id=1),nickname TEXT NOT NULL,avatar TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("初始化本机账户失败：%w", err)
	}
	if err := s.initModelShares(ctx); err != nil {
		return err
	}
	if err := s.initDepartments(ctx); err != nil {
		return err
	}
	// Requests, grants and remote boards remember the other side's avatar next to its name.
	for _, column := range []struct{ table, name string }{{"lan_requests", "avatar"}, {"lan_grants", "avatar"}, {"lan_outgoing", "owner_avatar"}, {"lan_outgoing", "owner_account"}, {"lan_grants", "viewer_fingerprint"}} {
		if err := s.addTextColumn(ctx, column.table, column.name); err != nil {
			return err
		}
	}
	return nil
}

func (s *AuthDB) addTextColumn(ctx context.Context, table, name string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, table, name).Scan(&exists); err != nil {
		return fmt.Errorf("检查 %s 表结构失败：%w", table, err)
	}
	if exists {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+name+` TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("升级 %s 表失败：%w", table, err)
	}
	return nil
}

func (s *AuthDB) Profile(ctx context.Context) (Profile, error) {
	var value Profile
	err := s.db.QueryRowContext(ctx, `SELECT nickname,avatar,created_at,updated_at FROM profile WHERE id=1`).Scan(&value.Nickname, &value.Avatar, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrNoProfile
	}
	if err != nil {
		return value, fmt.Errorf("读取本机账户失败：%w", err)
	}
	return value, nil
}

// CreateProfile keeps an account that already exists, so two starts racing never replace the
// first one; it returns whichever row is stored.
func (s *AuthDB) CreateProfile(ctx context.Context, value Profile) (Profile, error) {
	now := s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO profile(id,nickname,avatar,created_at,updated_at) VALUES(1,?,?,?,?)`, value.Nickname, value.Avatar, now, now); err != nil {
		return value, fmt.Errorf("创建本机账户失败：%w", err)
	}
	return s.Profile(ctx)
}

func (s *AuthDB) SaveProfile(ctx context.Context, nickname, avatar string) (Profile, error) {
	var value Profile
	err := s.db.QueryRowContext(ctx, `UPDATE profile SET nickname=?,avatar=?,updated_at=? WHERE id=1 RETURNING nickname,avatar,created_at,updated_at`, nickname, avatar, s.now().UnixMilli()).Scan(&value.Nickname, &value.Avatar, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrNoProfile
	}
	if err != nil {
		return value, fmt.Errorf("保存本机账户失败：%w", err)
	}
	return value, nil
}

// Model shares name the LAN members allowed to talk to 波西米亚 through this computer's model key. The key
// itself never leaves this computer: their requests come here and go out from here.
func (s *AuthDB) initModelShares(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS model_shares(member_id TEXT PRIMARY KEY,created_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("初始化模型共享失败：%w", err)
	}
	return nil
}

func (s *AuthDB) ModelShares(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT member_id FROM model_shares`)
	if err != nil {
		return nil, fmt.Errorf("读取模型共享失败：%w", err)
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取模型共享失败：%w", err)
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (s *AuthDB) SetModelShare(ctx context.Context, memberID string, shared bool) error {
	var err error
	if shared {
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO model_shares(member_id,created_at) VALUES(?,?)`, memberID, s.now().UnixMilli())
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM model_shares WHERE member_id=?`, memberID)
	}
	if err != nil {
		return fmt.Errorf("保存模型共享失败：%w", err)
	}
	return nil
}
