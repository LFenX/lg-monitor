package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type APIToken struct {
	ID         string `json:"id"`
	Hash       []byte `json:"-"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
	RevokedAt  int64  `json:"revokedAt,omitempty"`
}

type AuthDB struct {
	db  *sql.DB
	now func() time.Time
}

func OpenAuthDB(ctx context.Context, path string, now func() time.Time) (*AuthDB, error) {
	if path == "" || now == nil {
		return nil, fmt.Errorf("认证数据库需要路径和时钟")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析认证数据库路径失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("创建认证数据目录失败：%w", err)
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + uriPath
	}
	dsn := url.URL{Scheme: "file", Path: uriPath, RawQuery: url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)"}}.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("打开认证数据库失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS api_tokens(id TEXT PRIMARY KEY,token_hash BLOB NOT NULL UNIQUE CHECK(typeof(token_hash)='blob' AND length(token_hash)=32),name TEXT NOT NULL,scope TEXT NOT NULL CHECK(scope IN ('read','config','admin')),created_at INTEGER NOT NULL,last_used_at INTEGER NOT NULL DEFAULT 0,revoked_at INTEGER NOT NULL DEFAULT 0)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化认证数据库失败：%w", err)
	}
	return &AuthDB{db: db, now: now}, nil
}

func (s *AuthDB) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("关闭认证数据库失败：%w", err)
	}
	return nil
}

func (s *AuthDB) AddAPIToken(ctx context.Context, hash []byte, name, scope string) (APIToken, error) {
	name = strings.TrimSpace(name)
	if len(hash) != 32 || name == "" || len(name) > 128 || strings.ContainsFunc(name, unicode.IsControl) || (scope != "read" && scope != "config" && scope != "admin") {
		return APIToken{}, fmt.Errorf("API 令牌的哈希、名称或权限无效")
	}
	row := APIToken{ID: hex.EncodeToString(hash[:16]), Hash: append([]byte(nil), hash...), Name: name, Scope: scope, CreatedAt: s.now().UnixMilli()}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens(id,token_hash,name,scope,created_at) VALUES(?,?,?,?,?)`, row.ID, row.Hash, row.Name, row.Scope, row.CreatedAt); err != nil {
		return APIToken{}, fmt.Errorf("保存 API 令牌哈希失败：%w", err)
	}
	return row, nil
}

func (s *AuthDB) Authenticate(ctx context.Context, hash []byte) (APIToken, bool, error) {
	if len(hash) != 32 {
		return APIToken{}, false, fmt.Errorf("API 令牌哈希长度无效")
	}
	var row APIToken
	err := s.db.QueryRowContext(ctx, `UPDATE api_tokens SET last_used_at=max(last_used_at,?) WHERE token_hash=? AND revoked_at=0 RETURNING id,name,scope,created_at,last_used_at`, s.now().UnixMilli(), hash).Scan(&row.ID, &row.Name, &row.Scope, &row.CreatedAt, &row.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, fmt.Errorf("查询 API 令牌失败：%w", err)
	}
	row.Hash = append([]byte(nil), hash...)
	return row, true, nil
}

func (s *AuthDB) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,scope,created_at,last_used_at,revoked_at FROM api_tokens ORDER BY created_at,id`)
	if err != nil {
		return nil, fmt.Errorf("查询令牌清单失败：%w", err)
	}
	defer rows.Close()
	result := []APIToken{}
	for rows.Next() {
		var row APIToken
		if err := rows.Scan(&row.ID, &row.Name, &row.Scope, &row.CreatedAt, &row.LastUsedAt, &row.RevokedAt); err != nil {
			return nil, fmt.Errorf("读取令牌元数据失败：%w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历令牌清单失败：%w", err)
	}
	return result, nil
}

func (s *AuthDB) RevokeAPIToken(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=CASE WHEN revoked_at=0 THEN max(1,?) ELSE revoked_at END WHERE id=?`, s.now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("吊销令牌失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取令牌吊销结果失败：%w", err)
	}
	if count == 0 {
		return fmt.Errorf("令牌不存在，请先运行 lgmon token list")
	}
	return nil
}
