package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var (
	ErrTeamNotFound = errors.New("团队、成员或分享不存在，或当前身份无权访问")
	ErrTeamIdentity = errors.New("成员身份未登记或已吊销，请联系负责人重新邀请")
	ErrTeamInvite   = errors.New("邀请码错误、已过期或已使用，请联系负责人重新邀请")
	ErrTeamConflict = errors.New("分享已被修改或撤销，请刷新后重试")
	ErrTeamInput    = errors.New("团队参数无效")
	teamIdentifier  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
)

type Team struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerName string `json:"ownerName"`
	CreatedAt int64  `json:"createdAt"`
}
type TeamMember struct {
	ID          string `json:"id"`
	TeamID      string `json:"teamId"`
	Name        string `json:"name"`
	Serial      string `json:"-"`
	JoinedAt    int64  `json:"joinedAt"`
	RevokedAt   int64  `json:"revokedAt"`
	LastAccess  int64  `json:"lastAccess"`
	AccessCount int64  `json:"accessCount"`
}
type ShareScope struct {
	Collectors []string `json:"collectors,omitempty"`
	Targets    []string `json:"targets,omitempty"`
}
type TeamShare struct {
	ID          string   `json:"id"`
	TeamID      string   `json:"teamId"`
	DashboardID string   `json:"dashboardId"`
	Panels      []string `json:"panels,omitempty"`
	ShareScope
	Members   []string `json:"members,omitempty"`
	Revision  int64    `json:"revision"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
	RevokedAt int64    `json:"revokedAt"`
}
type JoinedTeam struct {
	ID          string `json:"id"`
	TeamID      string `json:"teamId"`
	TeamName    string `json:"teamName"`
	OwnerName   string `json:"ownerName"`
	MemberID    string `json:"memberId"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	JoinedAt    int64  `json:"joinedAt"`
	CA          []byte `json:"-"`
	Certificate []byte `json:"-"`
	KeyRef      string `json:"-"`
}

func (s *AuthDB) initTeams(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS teams(id TEXT PRIMARY KEY,name TEXT NOT NULL,owner_name TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS team_members(id TEXT PRIMARY KEY,team_id TEXT NOT NULL,name TEXT NOT NULL,serial TEXT NOT NULL UNIQUE,joined_at INTEGER NOT NULL,revoked_at INTEGER NOT NULL DEFAULT 0,last_access INTEGER NOT NULL DEFAULT 0,access_count INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS team_members_team ON team_members(team_id);
CREATE TABLE IF NOT EXISTS team_invites(hash TEXT PRIMARY KEY,team_id TEXT NOT NULL,name TEXT NOT NULL,expires INTEGER NOT NULL,used INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS team_shares(id TEXT PRIMARY KEY,team_id TEXT NOT NULL,dashboard_id TEXT NOT NULL,panels TEXT NOT NULL,collectors TEXT NOT NULL,targets TEXT NOT NULL,members TEXT NOT NULL,revision INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,revoked_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS team_shares_team ON team_shares(team_id);
CREATE TABLE IF NOT EXISTS team_joined(id TEXT PRIMARY KEY,team_id TEXT NOT NULL,team_name TEXT NOT NULL,owner_name TEXT NOT NULL,member_id TEXT NOT NULL,address TEXT NOT NULL,fingerprint TEXT NOT NULL,joined_at INTEGER NOT NULL,ca BLOB NOT NULL,certificate BLOB NOT NULL,key_ref TEXT NOT NULL,UNIQUE(fingerprint,team_id));`)
	if err != nil {
		return fmt.Errorf("初始化团队认证数据失败：%w", err)
	}
	return nil
}
func ValidTeamID(id string) bool { return teamIdentifier.MatchString(id) }
func validTeamName(name string) bool {
	return name != "" && len(name) <= 256 && strings.TrimSpace(name) == name && !strings.ContainsFunc(name, unicode.IsControl)
}
func validateTeamList(values []string, emptyAllowed bool) error {
	if values != nil && len(values) == 0 && !emptyAllowed || len(values) > 256 {
		return fmt.Errorf("列表须有 1～256 项，不限制时请省略：%w", ErrTeamInput)
	}
	seen := map[string]bool{}
	for _, id := range values {
		if !ValidTeamID(id) || seen[id] {
			return fmt.Errorf("标识须有效且不能重复或使用通配符：%w", ErrTeamInput)
		}
		seen[id] = true
	}
	return nil
}
func (s ShareScope) Validate() error {
	for _, value := range [][]string{s.Collectors, s.Targets} {
		if err := validateTeamList(value, false); err != nil {
			return err
		}
	}
	return nil
}
func (s *AuthDB) CreateTeam(ctx context.Context, value Team) (Team, error) {
	if !ValidTeamID(value.ID) || !validTeamName(value.Name) || !validTeamName(value.OwnerName) {
		return value, fmt.Errorf("团队标识、名称或负责人名称无效：%w", ErrTeamInput)
	}
	value.CreatedAt = s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO teams(id,name,owner_name,created_at) VALUES(?,?,?,?)`, value.ID, value.Name, value.OwnerName, value.CreatedAt); err != nil {
		return value, fmt.Errorf("保存团队失败：%w", err)
	}
	return value, nil
}
func (s *AuthDB) Team(ctx context.Context, id string) (Team, error) {
	var value Team
	err := s.db.QueryRowContext(ctx, `SELECT id,name,owner_name,created_at FROM teams WHERE id=?`, id).Scan(&value.ID, &value.Name, &value.OwnerName, &value.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrTeamNotFound
	}
	if err != nil {
		return value, fmt.Errorf("读取团队失败：%w", err)
	}
	return value, nil
}
func (s *AuthDB) Teams(ctx context.Context) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,owner_name,created_at FROM teams ORDER BY created_at,id`)
	if err != nil {
		return nil, fmt.Errorf("读取团队清单失败：%w", err)
	}
	defer rows.Close()
	values := []Team{}
	for rows.Next() {
		var v Team
		if err := rows.Scan(&v.ID, &v.Name, &v.OwnerName, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取团队字段失败：%w", err)
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历团队失败：%w", err)
	}
	return values, nil
}
func (s *AuthDB) AddTeamInvite(ctx context.Context, hash, team, name string, expires int64) error {
	if len(hash) != 64 || !validTeamName(name) || expires <= s.now().UnixMilli() {
		return fmt.Errorf("邀请名称、摘要或期限无效：%w", ErrTeamInput)
	}
	if _, err := s.Team(ctx, team); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO team_invites(hash,team_id,name,expires) VALUES(?,?,?,?)`, hash, team, name, expires); err != nil {
		return fmt.Errorf("保存邀请摘要失败：%w", err)
	}
	return nil
}
func (s *AuthDB) ConsumeTeamInvite(ctx context.Context, hash string, value TeamMember) (TeamMember, error) {
	if !ValidTeamID(value.ID) || !strings.HasPrefix(value.ID, "member-") || value.Serial == "" {
		return value, fmt.Errorf("成员证书身份无效：%w", ErrTeamInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return value, fmt.Errorf("开始加入团队事务失败：%w", err)
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `UPDATE team_invites SET used=1 WHERE hash=? AND used=0 AND expires>? RETURNING team_id,name`, hash, s.now().UnixMilli()).Scan(&value.TeamID, &value.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrTeamInvite
	}
	if err != nil {
		return value, fmt.Errorf("核验团队邀请失败：%w", err)
	}
	value.JoinedAt = s.now().UnixMilli()
	value.RevokedAt = 0
	value.LastAccess = 0
	value.AccessCount = 0
	if _, err = tx.ExecContext(ctx, `INSERT INTO team_members(id,team_id,name,serial,joined_at) VALUES(?,?,?,?,?)`, value.ID, value.TeamID, value.Name, value.Serial, value.JoinedAt); err != nil {
		return value, fmt.Errorf("登记团队成员失败：%w", err)
	}
	if err = tx.Commit(); err != nil {
		return value, fmt.Errorf("提交加入团队失败：%w", err)
	}
	return value, nil
}
func (s *AuthDB) TeamMember(ctx context.Context, id, serial string) (TeamMember, error) {
	var v TeamMember
	err := s.db.QueryRowContext(ctx, `SELECT id,team_id,name,serial,joined_at,revoked_at,last_access,access_count FROM team_members WHERE id=? AND serial=? AND revoked_at=0`, id, serial).Scan(&v.ID, &v.TeamID, &v.Name, &v.Serial, &v.JoinedAt, &v.RevokedAt, &v.LastAccess, &v.AccessCount)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrTeamIdentity
	}
	if err != nil {
		return v, fmt.Errorf("核验团队成员失败：%w", err)
	}
	return v, nil
}
func (s *AuthDB) TeamMembers(ctx context.Context, team string) ([]TeamMember, error) {
	if _, err := s.Team(ctx, team); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,team_id,name,serial,joined_at,revoked_at,last_access,access_count FROM team_members WHERE team_id=? ORDER BY joined_at,id`, team)
	if err != nil {
		return nil, fmt.Errorf("读取成员清单失败：%w", err)
	}
	defer rows.Close()
	values := []TeamMember{}
	for rows.Next() {
		var v TeamMember
		if err := rows.Scan(&v.ID, &v.TeamID, &v.Name, &v.Serial, &v.JoinedAt, &v.RevokedAt, &v.LastAccess, &v.AccessCount); err != nil {
			return nil, fmt.Errorf("读取成员字段失败：%w", err)
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历成员失败：%w", err)
	}
	return values, nil
}
func (s *AuthDB) RevokeTeamMember(ctx context.Context, team, id string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE team_members SET revoked_at=CASE WHEN revoked_at=0 THEN max(1,?) ELSE revoked_at END WHERE id=? AND team_id=?`, s.now().UnixMilli(), id, team)
	if err != nil {
		return fmt.Errorf("移除成员失败：%w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取成员移除结果失败：%w", err)
	}
	if n == 0 {
		return ErrTeamNotFound
	}
	return nil
}
func (s *AuthDB) VisitTeam(ctx context.Context, id, serial string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE team_members SET last_access=max(last_access,?),access_count=access_count+1 WHERE id=? AND serial=? AND revoked_at=0`, s.now().UnixMilli(), id, serial)
	if err != nil {
		return fmt.Errorf("登记团队访问失败：%w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取访问登记结果失败：%w", err)
	}
	if n == 0 {
		return ErrTeamIdentity
	}
	return nil
}
func shareVisible(share TeamShare, member TeamMember) bool {
	return share.RevokedAt == 0 && member.RevokedAt == 0 && share.TeamID == member.TeamID && (share.Members == nil || slices.Contains(share.Members, member.ID))
}
func (s *AuthDB) MemberShare(ctx context.Context, id string, member TeamMember) (TeamShare, error) {
	v, err := s.TeamShare(ctx, id)
	if err != nil {
		return v, err
	}
	if !shareVisible(v, member) {
		return TeamShare{}, ErrTeamNotFound
	}
	return v, nil
}
func jsonList(values []string) string { data, _ := json.Marshal(values); return string(data) }
