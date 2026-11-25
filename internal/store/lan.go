// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

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
	ID          string `json:"id"`
	MemberID    string `json:"memberId"`
	BoardID     string `json:"boardId"`
	DeviceName  string `json:"deviceName"`
	Avatar      string `json:"avatar,omitempty"`
	Hostname    string `json:"hostname"`
	CreatedAt   int64  `json:"createdAt"`
	RevokedAt   int64  `json:"revokedAt,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// Shared marks a board the owner gave an existing viewer without a request for it.
	Shared bool `json:"shared,omitempty"`
}

type LANOutgoing struct {
	ID string `json:"id"`
	// RemoteID is the owner's request, or the owner's grant when the board was given without a request.
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
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS lan_web_access(id INTEGER PRIMARY KEY CHECK(id=1),key_hash BLOB NOT NULL CHECK(typeof(key_hash)='blob' AND length(key_hash)=32),expires_at INTEGER NOT NULL CHECK(expires_at>=0));
CREATE TABLE IF NOT EXISTS lan_requests(id TEXT PRIMARY KEY,board_id TEXT NOT NULL,device_name TEXT NOT NULL,hostname TEXT NOT NULL,address TEXT NOT NULL,member_id TEXT NOT NULL,code TEXT NOT NULL,status TEXT NOT NULL,created_at INTEGER NOT NULL,decided_at INTEGER NOT NULL DEFAULT 0,csr BLOB,secret_hash BLOB NOT NULL,certificate BLOB);
CREATE INDEX IF NOT EXISTS lan_requests_status ON lan_requests(status,created_at);
CREATE TABLE IF NOT EXISTS lan_grants(id TEXT PRIMARY KEY,member_id TEXT NOT NULL,board_id TEXT NOT NULL,device_name TEXT NOT NULL,hostname TEXT NOT NULL,created_at INTEGER NOT NULL,revoked_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS lan_grants_member ON lan_grants(member_id,board_id);
CREATE TABLE IF NOT EXISTS lan_outgoing(id TEXT PRIMARY KEY,remote_id TEXT NOT NULL,address TEXT NOT NULL,owner_name TEXT NOT NULL,fingerprint TEXT NOT NULL,board_id TEXT NOT NULL,board_name TEXT NOT NULL,code TEXT NOT NULL,status TEXT NOT NULL,created_at INTEGER NOT NULL,joined_id TEXT NOT NULL DEFAULT '',ca BLOB NOT NULL,key_ref TEXT NOT NULL DEFAULT '',secret TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS lan_dismissed(remote_id TEXT PRIMARY KEY);`)
	if err != nil {
		return fmt.Errorf("初始化内网共享数据失败：%w", err)
	}
	return s.addTextColumn(ctx, "lan_web_access", "protected_key")
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

const lanGrantShared = "shared"

// ShareLANBoards gives boards to members who may already view another board of this computer, all or none. Only the
// member and board of each value count; who they are is copied from what they already hold. It returns the grants
// that are new, so saving the same choice again adds nothing.
func (s *AuthDB) ShareLANBoards(ctx context.Context, teamID string, values []LANGrant) ([]LANGrant, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始保存授权失败：%w", err)
	}
	defer tx.Rollback()
	now, added := s.now().UnixMilli(), []LANGrant{}
	for _, value := range values {
		// A request the member sent for this board is answered by the grant. Writing first also takes the write lock
		// before anything is read, so two saves cannot both find the board missing.
		if _, err := tx.ExecContext(ctx, `UPDATE lan_requests SET status=?,decided_at=? WHERE status=? AND member_id=? AND board_id=?`, LANApproved, now, LANPending, value.MemberID, value.BoardID); err != nil {
			return nil, fmt.Errorf("处理同一面板的申请失败：%w", err)
		}
		err := tx.QueryRowContext(ctx, `SELECT device_name,avatar,hostname,viewer_fingerprint FROM lan_grants WHERE member_id=? AND revoked_at=0
AND EXISTS (SELECT 1 FROM team_members m WHERE m.id=member_id AND m.team_id=? AND m.revoked_at=0) ORDER BY created_at DESC LIMIT 1`, value.MemberID, teamID).Scan(&value.DeviceName, &value.Avatar, &value.Hostname, &value.Fingerprint)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLANNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("核对查看者失败：%w", err)
		}
		value.CreatedAt, value.Shared = now, true
		result, err := tx.ExecContext(ctx, `INSERT INTO lan_grants(id,member_id,board_id,device_name,hostname,created_at,avatar,viewer_fingerprint,origin)
SELECT ?,?,?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM lan_grants WHERE member_id=? AND board_id=? AND revoked_at=0)`,
			value.ID, value.MemberID, value.BoardID, value.DeviceName, value.Hostname, now, value.Avatar, value.Fingerprint, lanGrantShared, value.MemberID, value.BoardID)
		if err != nil {
			return nil, fmt.Errorf("保存授权失败：%w", err)
		}
		if count, _ := result.RowsAffected(); count > 0 {
			added = append(added, value)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("保存授权失败：%w", err)
	}
	return added, nil
}

// SharedLANGrants are the boards a member was given without asking; the member learns about them from here.
func (s *AuthDB) SharedLANGrants(ctx context.Context, memberID string) ([]LANGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,board_id,created_at FROM lan_grants WHERE member_id=? AND origin=? AND revoked_at=0 ORDER BY created_at`, memberID, lanGrantShared)
	if err != nil {
		return nil, fmt.Errorf("读取直接授权失败：%w", err)
	}
	defer rows.Close()
	result := []LANGrant{}
	for rows.Next() {
		value := LANGrant{MemberID: memberID, Shared: true}
		if err := rows.Scan(&value.ID, &value.BoardID, &value.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取直接授权字段失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *AuthDB) LANGrants(ctx context.Context) ([]LANGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,member_id,board_id,device_name,hostname,created_at,revoked_at,avatar,viewer_fingerprint,origin='`+lanGrantShared+`' FROM lan_grants WHERE revoked_at=0 ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("读取授权失败：%w", err)
	}
	defer rows.Close()
	result := []LANGrant{}
	for rows.Next() {
		var value LANGrant
		if err := rows.Scan(&value.ID, &value.MemberID, &value.BoardID, &value.DeviceName, &value.Hostname, &value.CreatedAt, &value.RevokedAt, &value.Avatar, &value.Fingerprint, &value.Shared); err != nil {
			return nil, fmt.Errorf("读取授权字段失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// UpdateLANGrantViewer follows a viewer's nickname and avatar, which it reports when it reads a board.
func (s *AuthDB) UpdateLANGrantViewer(ctx context.Context, memberID, boardID, name, avatar, fingerprint string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE lan_grants SET device_name=?,avatar=?,viewer_fingerprint=?
WHERE member_id=? AND revoked_at=0 AND (device_name<>? OR avatar<>? OR viewer_fingerprint<>?)
AND EXISTS (SELECT 1 FROM lan_grants WHERE member_id=? AND board_id=? AND revoked_at=0)`,
		name, avatar, fingerprint, memberID, name, avatar, fingerprint, memberID, boardID)
	if err != nil {
		return false, fmt.Errorf("更新查看者资料失败：%w", err)
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// LANMemberAddresses is where each approved viewer asked from, by member; older viewers are recognised by it.
func (s *AuthDB) LANMemberAddresses(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT r.member_id,r.address FROM lan_requests r
WHERE r.status=? AND EXISTS (SELECT 1 FROM lan_grants g WHERE g.member_id=r.member_id AND g.board_id=r.board_id AND g.revoked_at=0)`, LANApproved)
	if err != nil {
		return nil, fmt.Errorf("读取申请地址失败：%w", err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var member, address string
		if err := rows.Scan(&member, &address); err != nil {
			return nil, fmt.Errorf("读取申请地址失败：%w", err)
		}
		if previous, ok := result[member]; ok && previous != address {
			address = ""
		}
		result[member] = address
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

// UpdateLANOutgoingOwner refreshes every board under the certificate fingerprint of the owner.
func (s *AuthDB) UpdateLANOutgoingOwner(ctx context.Context, fingerprint, name, avatar, account string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE lan_outgoing SET owner_name=?,owner_avatar=?,owner_account=coalesce(nullif(?,''),owner_account)
WHERE fingerprint=? AND (owner_name<>? OR owner_avatar<>? OR (?<>'' AND owner_account<>?))`,
		name, avatar, account, fingerprint, name, avatar, account, account)
	if err != nil {
		return false, fmt.Errorf("更新对方的昵称和头像失败：%w", err)
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// AdoptLANGrant records a board an owner gave this computer without a request; value.RemoteID is the owner's grant.
// It reports whether the list of boards changed: a board already here, or one its viewer removed, is left alone.
func (s *AuthDB) AdoptLANGrant(ctx context.Context, value LANOutgoing) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开始保存共享的面板失败：%w", err)
	}
	defer tx.Rollback()
	// An approved board follows its newest grant, so removing it later dismisses the grant that is in force. This
	// write also takes the lock before anything is read.
	if _, err := tx.ExecContext(ctx, `UPDATE lan_outgoing SET remote_id=? WHERE fingerprint=? AND board_id=? AND status=?`, value.RemoteID, value.Fingerprint, value.BoardID, LANApproved); err != nil {
		return false, fmt.Errorf("更新共享的面板失败：%w", err)
	}
	var known bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lan_outgoing WHERE fingerprint=? AND board_id=? AND status=?) OR EXISTS(SELECT 1 FROM lan_dismissed WHERE remote_id=?)`,
		value.Fingerprint, value.BoardID, LANApproved, value.RemoteID).Scan(&known); err != nil {
		return false, fmt.Errorf("检查共享的面板失败：%w", err)
	}
	if known {
		return false, tx.Commit()
	}
	var earlier string
	err = tx.QueryRowContext(ctx, `SELECT id FROM lan_outgoing WHERE fingerprint=? AND board_id=? ORDER BY created_at DESC LIMIT 1`, value.Fingerprint, value.BoardID).Scan(&earlier)
	switch {
	case err == nil:
		// A board that was refused, revoked or still waiting keeps its one place in the list.
		_, err = tx.ExecContext(ctx, `UPDATE lan_outgoing SET status=?,remote_id=?,joined_id=?,address=?,board_name=?,ca=?,code='' WHERE id=?`, LANApproved, value.RemoteID, value.JoinedID, value.Address, value.BoardName, value.CA, earlier)
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO lan_outgoing(`+lanOutgoingColumns+`) VALUES(?,?,?,?,?,?,?,'',?,?,?,?,'','',?,?)`,
			value.ID, value.RemoteID, value.Address, value.OwnerName, value.Fingerprint, value.BoardID, value.BoardName, LANApproved, s.now().UnixMilli(), value.JoinedID, value.CA, value.OwnerAvatar, value.OwnerAccount)
	}
	if err != nil {
		return false, fmt.Errorf("保存共享的面板失败：%w", err)
	}
	return true, tx.Commit()
}

// DeleteLANOutgoing removes a board or request from this computer's list. The owner is not told, so a board given
// without a request is remembered as removed and does not come back by itself.
func (s *AuthDB) DeleteLANOutgoing(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始删除申请记录失败：%w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO lan_dismissed(remote_id) SELECT remote_id FROM lan_outgoing WHERE id=? AND status=?`, id, LANApproved); err != nil {
		return fmt.Errorf("记录已移除的面板失败：%w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM lan_outgoing WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("删除发出的申请失败：%w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrLANNotFound
	}
	return tx.Commit()
}
