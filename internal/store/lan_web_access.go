// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type LANWebKey struct {
	Hash      []byte
	ExpiresAt int64
}

func (s *AuthDB) LANWebKey(ctx context.Context) (LANWebKey, error) {
	var key LANWebKey
	err := s.db.QueryRowContext(ctx, "SELECT key_hash, expires_at FROM lan_web_access WHERE id=1").Scan(&key.Hash, &key.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LANWebKey{}, nil
	}
	if err != nil {
		return LANWebKey{}, fmt.Errorf("读取内网完全访问密钥失败：%w", err)
	}
	return key, nil
}

func (s *AuthDB) SetLANWebKey(ctx context.Context, key LANWebKey) error {
	var err error
	if key.Hash == nil {
		_, err = s.db.ExecContext(ctx, "DELETE FROM lan_web_access WHERE id=1")
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO lan_web_access(id,key_hash,expires_at) VALUES(1,?,?)
            ON CONFLICT(id) DO UPDATE SET key_hash=excluded.key_hash,expires_at=excluded.expires_at`, key.Hash, key.ExpiresAt)
	}
	if err != nil {
		return fmt.Errorf("保存内网完全访问密钥失败：%w", err)
	}
	return nil
}

// An expired or revoked key must never be revived by a stale settings page.
func (s *AuthDB) ChangeLANKeyExpiry(ctx context.Context, expiresAt int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, "UPDATE lan_web_access SET expires_at=? WHERE id=1 AND (expires_at=0 OR expires_at>?)", expiresAt, s.now().UnixMilli())
	if err != nil {
		return false, fmt.Errorf("更新内网密钥有效期失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("核对内网密钥更新结果失败：%w", err)
	}
	return count != 0, nil
}
