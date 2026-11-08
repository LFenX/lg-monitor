package transport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var (
	ErrPairCode   = errors.New("配对码错误、已过期或已使用，请在监控器上生成新码")
	ErrRegistered = errors.New("该采集器已配对，请恢复原证书或先明确吊销后重新配对")
)

type Credential struct {
	ID       string                `json:"id"`
	Hostname string                `json:"hostname"`
	Serial   string                `json:"serial"`
	IssuedAt int64                 `json:"issuedAt"`
	Revoked  bool                  `json:"revoked"`
	Config   *CollectorConfigState `json:"config,omitempty"`
}

type PairRepository interface {
	AddCode(context.Context, string, int64) error
	Consume(context.Context, string, int64, Credential) error
	Find(context.Context, string) (Credential, bool, error)
	List(context.Context) ([]Credential, error)
	Revoke(context.Context, string) error
}

type PairDB struct{ db *sql.DB }

func OpenPairDB(ctx context.Context, path string) (*PairDB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析配对数据库路径失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("创建配对数据目录失败：%w", err)
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	uri.RawQuery = url.Values{"_pragma": {"journal_mode(WAL)", "busy_timeout(5000)"}, "_txlock": {"immediate"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("打开配对数据库失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pair_codes(hash TEXT PRIMARY KEY,expires INTEGER NOT NULL,used INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS identities(id TEXT PRIMARY KEY,hostname TEXT NOT NULL,serial TEXT NOT NULL,issued_at INTEGER NOT NULL,revoked INTEGER NOT NULL DEFAULT 0);`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化配对数据库失败：%w", err)
	}
	return &PairDB{db: db}, nil
}

func (p *PairDB) Close() error { return p.db.Close() }

func (p *PairDB) AddCode(ctx context.Context, hash string, expires int64) error {
	if len(hash) != 64 || expires <= 0 {
		return fmt.Errorf("配对码摘要或过期时间无效")
	}
	_, err := p.db.ExecContext(ctx, `INSERT INTO pair_codes(hash,expires) VALUES(?,?)`, hash, expires)
	if err != nil {
		return fmt.Errorf("保存配对码摘要失败：%w", err)
	}
	return nil
}

func (p *PairDB) Consume(ctx context.Context, hash string, now int64, credential Credential) error {
	if credential.ID == "" || credential.Serial == "" || credential.IssuedAt <= 0 || credential.Revoked {
		return fmt.Errorf("配对证书登记无效")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始配对事务失败：%w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE pair_codes SET used=1 WHERE hash=? AND used=0 AND expires>?`, hash, now)
	if err != nil {
		return fmt.Errorf("核验配对码失败：%w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取配对码状态失败：%w", err)
	}
	if changed != 1 {
		return ErrPairCode
	}
	var revoked bool
	err = tx.QueryRowContext(ctx, `SELECT revoked FROM identities WHERE id=?`, credential.ID).Scan(&revoked)
	if err == nil && !revoked {
		return ErrRegistered
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("检查原配对身份失败：%w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identities(id,hostname,serial,issued_at,revoked) VALUES(?,?,?,?,0) ON CONFLICT(id) DO UPDATE SET hostname=excluded.hostname,serial=excluded.serial,issued_at=excluded.issued_at,revoked=0`, credential.ID, credential.Hostname, credential.Serial, credential.IssuedAt)
	if err != nil {
		return fmt.Errorf("登记配对证书失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交配对事务失败：%w", err)
	}
	return nil
}

func (p *PairDB) Find(ctx context.Context, id string) (Credential, bool, error) {
	var item Credential
	err := p.db.QueryRowContext(ctx, `SELECT id,hostname,serial,issued_at,revoked FROM identities WHERE id=?`, id).Scan(&item.ID, &item.Hostname, &item.Serial, &item.IssuedAt, &item.Revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, fmt.Errorf("读取配对身份失败：%w", err)
	}
	return item, true, nil
}

func (p *PairDB) List(ctx context.Context) ([]Credential, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id,hostname,serial,issued_at,revoked FROM identities ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("列出配对采集器失败：%w", err)
	}
	defer rows.Close()
	items := make([]Credential, 0)
	for rows.Next() {
		var item Credential
		if err := rows.Scan(&item.ID, &item.Hostname, &item.Serial, &item.IssuedAt, &item.Revoked); err != nil {
			return nil, fmt.Errorf("读取配对采集器失败：%w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历配对采集器失败：%w", err)
	}
	return items, nil
}

func (p *PairDB) Revoke(ctx context.Context, id string) error {
	result, err := p.db.ExecContext(ctx, `UPDATE identities SET revoked=1 WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("吊销采集器失败：%w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取吊销结果失败：%w", err)
	}
	if changed == 0 {
		return fmt.Errorf("采集器不存在，请先运行 collectors list")
	}
	return nil
}
