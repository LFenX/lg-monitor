package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LAN sharing keeps three kinds of records next to the team tables: requests someone sent to
// this computer, grants this computer gave, and requests this computer sent to others.

const (
	LANPending  = "pending"
	LANApproved = "approved"
	LANDenied   = "denied"
	LANExpired  = "expired"
	// LANRevoked is only recorded by the viewer, when the owner refuses a board it had granted.
	LANRevoked = "revoked"
)

var ErrLANNotFound = errors.New("申请或授权不存在")

type LANRequest struct {
	ID          string `json:"id"`
	BoardID     string `json:"boardId"`
	DeviceName  string `json:"deviceName"`
	Avatar      string `json:"avatar,omitempty"`
	Hostname    string `json:"hostname"`
	Address     string `json:"address"`
	MemberID    string `json:"memberId,omitempty"`
	Code        string `json:"code"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"createdAt"`
	DecidedAt   int64  `json:"decidedAt,omitempty"`
	CSR         []byte `json:"-"`
	SecretHash  []byte `json:"-"`
	Certificate []byte `json:"-"`
}

type LANGrant struct {
	ID         string `json:"id"`
	MemberID   string `json:"memberId"`
	BoardID    string `json:"boardId"`
	DeviceName string `json:"deviceName"`
	Avatar     string `json:"avatar,omitempty"`
	Hostname   string `json:"hostname"`
	CreatedAt  int64  `json:"createdAt"`
	RevokedAt  int64  `json:"revokedAt,omitempty"`
}

type LANOutgoing struct {
	ID          string `json:"id"`
	RemoteID    string `json:"-"`
	Address     string `json:"address"`
	OwnerName   string `json:"ownerName"`
	OwnerAvatar string `json:"ownerAvatar,omitempty"`
	// OwnerAccount is the owner's account key, which places them in departments.
	OwnerAccount string `json:"ownerAccount,omitempty"`
	Fingerprint  string `json:"fingerprint"`
	BoardID      string `json:"boardId"`
	BoardName    string `json:"boardName"`
	Code         string `json:"code"`
	Status       string `json:"status"`
	CreatedAt    int64  `json:"createdAt"`
	JoinedID     string `json:"-"`
	CA           []byte `json:"-"`
	KeyRef       string `json:"-"`
	Secret       string `json:"-"`
}

func (s *AuthDB) initLAN(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS lan_requests(id TEXT PRIMARY KEY,board_id TEXT NOT NULL,device_name TEXT NOT NULL,hostname TEXT NOT NULL,address TEXT NOT NULL,member_id TEXT NOT NULL,code TEXT NOT NULL,status TEXT NOT NULL,created_at INTEGER NOT NULL,decided_at INTEGER NOT NULL DEFAULT 0,csr BLOB,secret_hash BLOB NOT NULL,certificate BLOB);
CREATE INDEX IF NOT EXISTS lan_requests_status ON lan_requests(status,created_at);
CREATE TABLE IF NOT EXISTS lan_grants(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,board_id TEXT NOT NULL,device_name TEXT NOT NULL,hostname TEXT NOT NULL,created_at INTEGER NOT NULL,revoked_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS lan_grants_member ON lan_grants(member_id,board_id);
CREATE TABLE IF NOT EXISTS lan_outgoing(id TEXT PRIMARY KEY,remote_id TEXT NOT NULL,address TEXT NOT NULL,owner_name TEXT NOT NULL,fingerprint TEXT NOT NULL,board_id TEXT NOT NULL,board_name TEXT NOT NULL,code TEXT NOT NULL,status TEXT NOT NULL,created_at INTEGER NOT NULL,joined_id TEXT NOT NULL DEFAULT '',ca BLOB NOT NULL,key_ref TEXT NOT NULL DEFAULT '',secret TEXT NOT NULL);`)
	if err != nil {
		return fmt.Errorf("初始化内网共享数据失败：%w", err)
	}
	return nil
}

func scanLANRequest(row interface{ Scan(...any) error }) (LANRequest, error) {
	var value LANRequest
	err := row.Scan(&value.ID, &value.BoardID, &value.DeviceName, &value.Hostname, &value.Address, &value.MemberID, &value.Code, &value.Status, &value.CreatedAt, &value.DecidedAt, &value.CSR, &value.SecretHash, &value.Certificate, &value.Avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrLANNotFound
	}
	return value, err
}

const lanRequestColumns = `id,board_id,device_name,hostname,address,member_id,code,status,created_at,decided_at,csr,secret_hash,certificate,avatar`

// AddLANRequest stores a request unless too many are already waiting, which keeps a noisy
// network from filling the list faster than a person can read it.
func (s *AuthDB) AddLANRequest(ctx context.Context, value LANRequest, maxPending int, notBefore int64) (LANRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return value, fmt.Errorf("开始保存申请失败：%w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE lan_requests SET status=? WHERE status=? AND created_at<?`, LANExpired, LANPending, notBefore); err != nil {
		return value, fmt.Errorf("清理过期申请失败：%w", err)
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM lan_requests WHERE status=?`, LANPending).Scan(&pending); err != nil {
		return value, fmt.Errorf("统计待处理申请失败：%w", err)
	}
	if pending >= maxPending {
		return value, fmt.Errorf("待处理的申请太多了，请对方稍后再试：%w", ErrTeamInput)
	}
	value.Status, value.CreatedAt = LANPending, s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO lan_requests(`+lanRequestColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.BoardID, value.DeviceName, value.Hostname, value.Address, value.MemberID, value.Code, value.Status, value.CreatedAt, 0, value.CSR, value.SecretHash, value.Certificate, value.Avatar); err != nil {
		return value, fmt.Errorf("保存申请失败：%w", err)
	}
	return value, tx.Commit()
}

func (s *AuthDB) LANRequest(ctx context.Context, id string) (LANRequest, error) {
	return scanLANRequest(s.db.QueryRowContext(ctx, `SELECT `+lanRequestColumns+` FROM lan_requests WHERE id=?`, id))
}

// LANRequests lists requests of the last week, newest first; pending ones past notBefore expire.
func (s *AuthDB) LANRequests(ctx context.Context, notBefore, since int64) ([]LANRequest, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE lan_requests SET status=? WHERE status=? AND created_at<?`, LANExpired, LANPending, notBefore); err != nil {
		return nil, fmt.Errorf("清理过期申请失败：%w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+lanRequestColumns+` FROM lan_requests WHERE created_at>=? ORDER BY created_at DESC LIMIT 200`, since)
	if err != nil {
		return nil, fmt.Errorf("读取申请失败：%w", err)
	}
	defer rows.Close()
	result := []LANRequest{}
	for rows.Next() {
		value, err := scanLANRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("读取申请字段失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// DecideLANRequest settles a pending request once; a second decision finds nothing to change.
func (s *AuthDB) DecideLANRequest(ctx context.Context, id, status, memberID string, certificate []byte) error {
	result, err := s.db.ExecContext(ctx, `UPDATE lan_requests SET status=?,member_id=?,certificate=?,decided_at=? WHERE id=? AND status=?`, status, memberID, certificate, s.now().UnixMilli(), id, LANPending)
	if err != nil {
		return fmt.Errorf("保存决定失败：%w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("这个申请已经处理过或已过期：%w", ErrLANNotFound)
	}
	return nil
}

// AddTeamMember registers a device approved in person rather than through an invitation code.
func (s *AuthDB) AddTeamMember(ctx context.Context, value TeamMember) (TeamMember, error) {
	if !ValidTeamID(value.ID) || value.Serial == "" || !ValidTeamID(value.TeamID) {
		return value, fmt.Errorf("成员身份无效：%w", ErrTeamInput)
	}
	value.JoinedAt = s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO team_members(id,team_id,name,serial,joined_at) VALUES(?,?,?,?,?)`, value.ID, value.TeamID, value.Name, value.Serial, value.JoinedAt); err != nil {
		return value, fmt.Errorf("登记成员失败：%w", err)
	}
	return value, nil
}

func (s *AuthDB) AddLANGrant(ctx context.Context, value LANGrant) (LANGrant, error) {
	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM lan_grants WHERE member_id=? AND board_id=? AND revoked_at=0`, value.MemberID, value.BoardID).Scan(&existing)
	if err == nil {
		value.ID = existing
		return value, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return value, fmt.Errorf("检查已有授权失败：%w", err)
	}
	value.CreatedAt = s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO lan_grants(id,member_id,board_id,device_name,hostname,created_at,avatar) VALUES(?,?,?,?,?,?,?)`, value.ID, value.MemberID, value.BoardID, value.DeviceName, value.Hostname, value.CreatedAt, value.Avatar); err != nil {
		return value, fmt.Errorf("保存授权失败：%w", err)
	}
	return value, nil
}

func (s *AuthDB) LANGrants(ctx context.Context) ([]LANGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,member_id,board_id,device_name,hostname,created_at,revoked_at,avatar FROM lan_grants WHERE revoked_at=0 ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("读取授权失败：%w", err)
	}
	defer rows.Close()
	result := []LANGrant{}
	for rows.Next() {
		var value LANGrant
		if err := rows.Scan(&value.ID, &value.MemberID, &value.BoardID, &value.DeviceName, &value.Hostname, &value.CreatedAt, &value.RevokedAt, &value.Avatar); err != nil {
			return nil, fmt.Errorf("读取授权字段失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *AuthDB) LANGranted(ctx context.Context, memberID, boardID string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM lan_grants WHERE member_id=? AND board_id=? AND revoked_at=0`, memberID, boardID).Scan(&count); err != nil {
		return false, fmt.Errorf("检查授权失败：%w", err)
	}
	return count > 0, nil
}

func (s *AuthDB) RevokeLANGrant(ctx context.Context, id string) (LANGrant, error) {
	var value LANGrant
	err := s.db.QueryRowContext(ctx, `UPDATE lan_grants SET revoked_at=? WHERE id=? AND revoked_at=0 RETURNING id,member_id,board_id,device_name,hostname,created_at,revoked_at,avatar`, s.now().UnixMilli(), id).Scan(&value.ID, &value.MemberID, &value.BoardID, &value.DeviceName, &value.Hostname, &value.CreatedAt, &value.RevokedAt, &value.Avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrLANNotFound
	}
	if err != nil {
		return value, fmt.Errorf("撤销授权失败：%w", err)
	}
	return value, nil
}

const lanOutgoingColumns = `id,remote_id,address,owner_name,fingerprint,board_id,board_name,code,status,created_at,joined_id,ca,key_ref,secret,owner_avatar,owner_account`

func scanLANOutgoing(row interface{ Scan(...any) error }) (LANOutgoing, error) {
	var value LANOutgoing
	err := row.Scan(&value.ID, &value.RemoteID, &value.Address, &value.OwnerName, &value.Fingerprint, &value.BoardID, &value.BoardName, &value.Code, &value.Status, &value.CreatedAt, &value.JoinedID, &value.CA, &value.KeyRef, &value.Secret, &value.OwnerAvatar, &value.OwnerAccount)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrLANNotFound
	}
	return value, err
}

func (s *AuthDB) AddLANOutgoing(ctx context.Context, value LANOutgoing) (LANOutgoing, error) {
	value.CreatedAt = s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO lan_outgoing(`+lanOutgoingColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.RemoteID, value.Address, value.OwnerName, value.Fingerprint, value.BoardID, value.BoardName, value.Code, value.Status, value.CreatedAt, value.JoinedID, value.CA, value.KeyRef, value.Secret, value.OwnerAvatar, value.OwnerAccount); err != nil {
		return value, fmt.Errorf("保存发出的申请失败：%w", err)
	}
	return value, nil
}

func (s *AuthDB) LANOutgoingItem(ctx context.Context, id string) (LANOutgoing, error) {
	return scanLANOutgoing(s.db.QueryRowContext(ctx, `SELECT `+lanOutgoingColumns+` FROM lan_outgoing WHERE id=?`, id))
}

func (s *AuthDB) LANOutgoing(ctx context.Context) ([]LANOutgoing, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+lanOutgoingColumns+` FROM lan_outgoing ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("读取发出的申请失败：%w", err)
	}
	defer rows.Close()
	result := []LANOutgoing{}
	for rows.Next() {
		value, err := scanLANOutgoing(rows)
		if err != nil {
			return nil, fmt.Errorf("读取发出的申请字段失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *AuthDB) UpdateLANOutgoing(ctx context.Context, id, status, joinedID, address string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE lan_outgoing SET status=?,joined_id=?,address=? WHERE id=?`, status, joinedID, address, id); err != nil {
		return fmt.Errorf("更新发出的申请失败：%w", err)
	}
	return nil
}

// UpdateLANOutgoingOwner follows the owner's nickname and avatar, which can change after approval.
func (s *AuthDB) UpdateLANOutgoingOwner(ctx context.Context, id, name, avatar, account string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE lan_outgoing SET owner_name=?,owner_avatar=?,owner_account=? WHERE id=?`, name, avatar, account, id); err != nil {
		return fmt.Errorf("更新对方的昵称和头像失败：%w", err)
	}
	return nil
}

func (s *AuthDB) DeleteLANOutgoing(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM lan_outgoing WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("删除发出的申请失败：%w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrLANNotFound
	}
	return nil
}
