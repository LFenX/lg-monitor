package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/config"
)

const shareColumns = `id,team_id,dashboard_id,panels,collectors,targets,members,revision,created_at,updated_at,revoked_at`

func scanShare(row interface{ Scan(...any) error }) (TeamShare, error) {
	var value TeamShare
	var panels, collectors, targets, members string
	if err := row.Scan(&value.ID, &value.TeamID, &value.DashboardID, &panels, &collectors, &targets, &members, &value.Revision, &value.CreatedAt, &value.UpdatedAt, &value.RevokedAt); err != nil {
		return value, err
	}
	for _, field := range []struct {
		source string
		out    *[]string
	}{{panels, &value.Panels}, {collectors, &value.Collectors}, {targets, &value.Targets}, {members, &value.Members}} {
		if err := json.Unmarshal([]byte(field.source), field.out); err != nil {
			return value, fmt.Errorf("解析分享范围失败：%w", err)
		}
	}
	return value, nil
}

func (s *AuthDB) TeamShare(ctx context.Context, id string) (TeamShare, error) {
	value, err := scanShare(s.db.QueryRowContext(ctx, `SELECT `+shareColumns+` FROM team_shares WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrTeamNotFound
	}
	if err != nil {
		return value, fmt.Errorf("读取分享失败：%w", err)
	}
	return value, nil
}

func (s *AuthDB) TeamShares(ctx context.Context, team string) ([]TeamShare, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+shareColumns+` FROM team_shares WHERE (?='' OR team_id=?) ORDER BY created_at,id`, team, team)
	if err != nil {
		return nil, fmt.Errorf("读取分享清单失败：%w", err)
	}
	defer rows.Close()
	values := []TeamShare{}
	for rows.Next() {
		value, err := scanShare(rows)
		if err != nil {
			return nil, fmt.Errorf("读取分享字段失败：%w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历分享失败：%w", err)
	}
	return values, nil
}

func (s *AuthDB) SaveTeamShare(ctx context.Context, value TeamShare) (TeamShare, error) {
	if !ValidTeamID(value.ID) || !ValidTeamID(value.TeamID) || !config.ValidID(value.DashboardID) {
		return value, fmt.Errorf("分享标识无效：%w", ErrTeamInput)
	}
	if err := value.ShareScope.Validate(); err != nil {
		return value, err
	}
	if err := validateTeamList(value.Panels, true, config.ValidID); err != nil {
		return value, err
	}
	if err := validateTeamList(value.Members, false, ValidTeamID); err != nil {
		return value, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return value, fmt.Errorf("开始保存分享事务失败：%w", err)
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=?)`, value.TeamID).Scan(&exists); err != nil {
		return value, fmt.Errorf("检查分享团队失败：%w", err)
	}
	if !exists {
		return value, ErrTeamNotFound
	}
	for _, id := range value.Members {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM team_members WHERE id=? AND team_id=? AND revoked_at=0)`, id, value.TeamID).Scan(&exists); err != nil {
			return value, fmt.Errorf("检查分享成员失败：%w", err)
		}
		if !exists {
			return value, fmt.Errorf("指定成员须属于该团队且尚未吊销：%w", ErrTeamInput)
		}
	}
	at := s.now().UnixMilli()
	if value.Revision == 0 {
		value.Revision, value.CreatedAt, value.UpdatedAt, value.RevokedAt = 1, at, at, 0
		_, err = tx.ExecContext(ctx, `INSERT INTO team_shares(`+shareColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,0)`, value.ID, value.TeamID, value.DashboardID, jsonList(value.Panels), jsonList(value.Collectors), jsonList(value.Targets), jsonList(value.Members), value.Revision, at, at)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE team_shares SET dashboard_id=?,panels=?,collectors=?,targets=?,members=?,revision=revision+1,updated_at=? WHERE id=? AND team_id=? AND revision=? AND revoked_at=0`, value.DashboardID, jsonList(value.Panels), jsonList(value.Collectors), jsonList(value.Targets), jsonList(value.Members), at, value.ID, value.TeamID, value.Revision)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				return value, ErrTeamConflict
			}
		}
	}
	if err != nil {
		return value, fmt.Errorf("保存分享失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return value, fmt.Errorf("提交分享失败：%w", err)
	}
	return s.TeamShare(ctx, value.ID)
}

func (s *AuthDB) RevokeTeamShare(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE team_shares SET revoked_at=CASE WHEN revoked_at=0 THEN max(1,?) ELSE revoked_at END,updated_at=CASE WHEN revoked_at=0 THEN ? ELSE updated_at END,revision=revision+CASE WHEN revoked_at=0 THEN 1 ELSE 0 END WHERE id=?`, s.now().UnixMilli(), s.now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("撤销分享失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取撤销分享结果失败：%w", err)
	}
	if count == 0 {
		return ErrTeamNotFound
	}
	return nil
}

const joinedColumns = `id,team_id,team_name,owner_name,member_id,address,fingerprint,joined_at,ca,certificate,key_ref`

func scanJoined(row interface{ Scan(...any) error }) (JoinedTeam, error) {
	var value JoinedTeam
	err := row.Scan(&value.ID, &value.TeamID, &value.TeamName, &value.OwnerName, &value.MemberID, &value.Address, &value.Fingerprint, &value.JoinedAt, &value.CA, &value.Certificate, &value.KeyRef)
	return value, err
}

func (s *AuthDB) JoinedTeams(ctx context.Context) ([]JoinedTeam, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+joinedColumns+` FROM team_joined ORDER BY joined_at,id`)
	if err != nil {
		return nil, fmt.Errorf("读取本机团队身份失败：%w", err)
	}
	defer rows.Close()
	values := []JoinedTeam{}
	for rows.Next() {
		value, err := scanJoined(rows)
		if err != nil {
			return nil, fmt.Errorf("读取本机身份字段失败：%w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历本机团队身份失败：%w", err)
	}
	return values, nil
}

func (s *AuthDB) JoinedTeam(ctx context.Context, id string) (JoinedTeam, error) {
	value, err := scanJoined(s.db.QueryRowContext(ctx, `SELECT `+joinedColumns+` FROM team_joined WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrTeamNotFound
	}
	if err != nil {
		return value, fmt.Errorf("读取本机团队身份失败：%w", err)
	}
	return value, nil
}

func (s *AuthDB) AddJoinedTeam(ctx context.Context, value JoinedTeam) (JoinedTeam, error) {
	if !ValidTeamID(value.ID) || !ValidTeamID(value.TeamID) || !ValidTeamID(value.MemberID) || value.Address == "" || len(value.Fingerprint) != 64 || len(value.CA) == 0 || len(value.CA) > 64*1024 || len(value.Certificate) == 0 || len(value.Certificate) > 64*1024 || value.KeyRef == "" {
		return value, fmt.Errorf("本机团队身份字段无效：%w", ErrTeamInput)
	}
	value.JoinedAt = s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO team_joined(`+joinedColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.TeamID, value.TeamName, value.OwnerName, value.MemberID, value.Address, value.Fingerprint, value.JoinedAt, value.CA, value.Certificate, value.KeyRef)
	if err != nil {
		return value, fmt.Errorf("保存本机团队身份失败：%w", err)
	}
	return value, nil
}

func (s *AuthDB) ChangeJoinedAddress(ctx context.Context, id, address string) (JoinedTeam, error) {
	if _, err := s.JoinedTeam(ctx, id); err != nil {
		return JoinedTeam{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE team_joined SET address=? WHERE id=?`, address, id); err != nil {
		return JoinedTeam{}, fmt.Errorf("保存负责人新地址失败：%w", err)
	}
	return s.JoinedTeam(ctx, id)
}

func (s *AuthDB) LeaveJoinedTeam(ctx context.Context, id string) error {
	if _, err := s.JoinedTeam(ctx, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM team_joined WHERE id=?`, id); err != nil {
		return fmt.Errorf("移除本机团队身份失败：%w", err)
	}
	return nil
}
