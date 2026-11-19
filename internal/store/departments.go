package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

/*
 * Departments this computer hosts, the people in them and the requests to join. A department lives on the computer
 * that created it; everyone else on the LAN reads it from there. People are known by their computer's account key
 * (Ed25519), so a leader on any computer can approve with a signed request.
 */

const (
	DepartmentLeader = "leader"
	DepartmentMember = "member"
)

var ErrDepartmentMissing = errors.New("部门不存在")

type Department struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar,omitempty"`
	Parent    string `json:"parent,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type DepartmentPerson struct {
	Department string `json:"-"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar,omitempty"`
	Role       string `json:"role"`
	JoinedAt   int64  `json:"joinedAt"`
}

type DepartmentRequest struct {
	ID         string `json:"id"`
	Department string `json:"department"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar,omitempty"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"createdAt"`
}

func (s *AuthDB) initDepartments(ctx context.Context) error {
	if err := s.addBlobColumn(ctx, "profile", "account_key"); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS departments(id TEXT PRIMARY KEY,name TEXT NOT NULL,avatar TEXT NOT NULL DEFAULT '',parent TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS department_people(department TEXT NOT NULL,key TEXT NOT NULL,name TEXT NOT NULL,avatar TEXT NOT NULL DEFAULT '',role TEXT NOT NULL CHECK(role IN ('leader','member')),joined_at INTEGER NOT NULL,PRIMARY KEY(department,key));
CREATE TABLE IF NOT EXISTS department_requests(id TEXT PRIMARY KEY,department TEXT NOT NULL,key TEXT NOT NULL,name TEXT NOT NULL,avatar TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,created_at INTEGER NOT NULL);`)
	if err != nil {
		return fmt.Errorf("初始化部门失败：%w", err)
	}
	return nil
}

func (s *AuthDB) addBlobColumn(ctx context.Context, table, name string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, table, name).Scan(&exists); err != nil {
		return fmt.Errorf("检查 %s 表结构失败：%w", table, err)
	}
	if exists {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+name+` BLOB`); err != nil {
		return fmt.Errorf("升级 %s 表失败：%w", table, err)
	}
	return nil
}

// AccountKey is this computer's signing key for departments, made the first time it is needed.
func (s *AuthDB) AccountKey(ctx context.Context) (ed25519.PrivateKey, error) {
	var seed []byte
	err := s.db.QueryRowContext(ctx, `SELECT account_key FROM profile WHERE id=1`).Scan(&seed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoProfile
	}
	if err != nil {
		return nil, fmt.Errorf("读取账户密钥失败：%w", err)
	}
	if len(seed) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(seed), nil
	}
	seed = make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("生成账户密钥失败：%w", err)
	}
	// A key written by a concurrent call wins; read back whichever was stored.
	if _, err := s.db.ExecContext(ctx, `UPDATE profile SET account_key=? WHERE id=1 AND (account_key IS NULL OR length(account_key)=0)`, seed); err != nil {
		return nil, fmt.Errorf("保存账户密钥失败：%w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT account_key FROM profile WHERE id=1`).Scan(&seed); err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("读取账户密钥失败：%w", err)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func (s *AuthDB) Departments(ctx context.Context) ([]Department, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,avatar,parent,created_at,updated_at FROM departments ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("读取部门失败：%w", err)
	}
	defer rows.Close()
	result := []Department{}
	for rows.Next() {
		var value Department
		if err := rows.Scan(&value.ID, &value.Name, &value.Avatar, &value.Parent, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取部门失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *AuthDB) Department(ctx context.Context, id string) (Department, error) {
	var value Department
	err := s.db.QueryRowContext(ctx, `SELECT id,name,avatar,parent,created_at,updated_at FROM departments WHERE id=?`, id).Scan(&value.ID, &value.Name, &value.Avatar, &value.Parent, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrDepartmentMissing
	}
	return value, err
}

// SaveDepartment creates or changes a department; a new one starts with its creator as leader.
func (s *AuthDB) SaveDepartment(ctx context.Context, value Department, creator *DepartmentPerson) (Department, error) {
	now := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return value, fmt.Errorf("保存部门失败：%w", err)
	}
	defer tx.Rollback()
	if creator != nil {
		value.CreatedAt = now
		if _, err := tx.ExecContext(ctx, `INSERT INTO departments(id,name,avatar,parent,created_at,updated_at) VALUES(?,?,?,?,?,?)`, value.ID, value.Name, value.Avatar, value.Parent, now, now); err != nil {
			return value, fmt.Errorf("保存部门失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO department_people(department,key,name,avatar,role,joined_at) VALUES(?,?,?,?,?,?)`, value.ID, creator.Key, creator.Name, creator.Avatar, DepartmentLeader, now); err != nil {
			return value, fmt.Errorf("保存部门负责人失败：%w", err)
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE departments SET name=?,avatar=?,parent=?,updated_at=? WHERE id=?`, value.Name, value.Avatar, value.Parent, now, value.ID)
		if err != nil {
			return value, fmt.Errorf("保存部门失败：%w", err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return value, ErrDepartmentMissing
		}
	}
	value.UpdatedAt = now
	return value, tx.Commit()
}

func (s *AuthDB) DeleteDepartment(ctx context.Context, id string) error {
	for _, statement := range []string{`DELETE FROM department_requests WHERE department=?`, `DELETE FROM department_people WHERE department=?`, `DELETE FROM departments WHERE id=?`} {
		if _, err := s.db.ExecContext(ctx, statement, id); err != nil {
			return fmt.Errorf("删除部门失败：%w", err)
		}
	}
	// Sub-departments here move up to the top level rather than pointing at nothing.
	if _, err := s.db.ExecContext(ctx, `UPDATE departments SET parent='' WHERE parent=? OR parent LIKE ?`, id, "%:"+id); err != nil {
		return fmt.Errorf("删除部门失败：%w", err)
	}
	return nil
}

func (s *AuthDB) DepartmentPeople(ctx context.Context, id string) ([]DepartmentPerson, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT department,key,name,avatar,role,joined_at FROM department_people WHERE department=? ORDER BY role='member', joined_at`, id)
	if err != nil {
		return nil, fmt.Errorf("读取部门成员失败：%w", err)
	}
	defer rows.Close()
	result := []DepartmentPerson{}
	for rows.Next() {
		var value DepartmentPerson
		if err := rows.Scan(&value.Department, &value.Key, &value.Name, &value.Avatar, &value.Role, &value.JoinedAt); err != nil {
			return nil, fmt.Errorf("读取部门成员失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *AuthDB) SetDepartmentPerson(ctx context.Context, value DepartmentPerson) error {
	value.JoinedAt = s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO department_people(department,key,name,avatar,role,joined_at) VALUES(?,?,?,?,?,?) ON CONFLICT(department,key) DO UPDATE SET name=excluded.name,avatar=excluded.avatar,role=excluded.role`, value.Department, value.Key, value.Name, value.Avatar, value.Role, value.JoinedAt)
	if err != nil {
		return fmt.Errorf("保存部门成员失败：%w", err)
	}
	return nil
}

func (s *AuthDB) RemoveDepartmentPerson(ctx context.Context, department, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM department_people WHERE department=? AND key=?`, department, key); err != nil {
		return fmt.Errorf("移出部门失败：%w", err)
	}
	return nil
}

// AddDepartmentRequest records one pending request per person and department; asking again refreshes it.
func (s *AuthDB) AddDepartmentRequest(ctx context.Context, value DepartmentRequest) (DepartmentRequest, error) {
	value.Status, value.CreatedAt = LANPending, s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM department_requests WHERE department=? AND key=? AND status=?`, value.Department, value.Key, LANPending); err != nil {
		return value, fmt.Errorf("保存加入申请失败：%w", err)
	}
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM department_requests WHERE status=?`, LANPending).Scan(&pending); err != nil {
		return value, fmt.Errorf("保存加入申请失败：%w", err)
	}
	if pending >= 200 {
		return value, fmt.Errorf("待处理的加入申请太多了，请稍后再试：%w", ErrTeamInput)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO department_requests(id,department,key,name,avatar,status,created_at) VALUES(?,?,?,?,?,?,?)`, value.ID, value.Department, value.Key, value.Name, value.Avatar, value.Status, value.CreatedAt); err != nil {
		return value, fmt.Errorf("保存加入申请失败：%w", err)
	}
	return value, nil
}

func (s *AuthDB) DepartmentRequests(ctx context.Context, department string) ([]DepartmentRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,department,key,name,avatar,status,created_at FROM department_requests WHERE department=? AND status=? ORDER BY created_at`, department, LANPending)
	if err != nil {
		return nil, fmt.Errorf("读取加入申请失败：%w", err)
	}
	defer rows.Close()
	result := []DepartmentRequest{}
	for rows.Next() {
		var value DepartmentRequest
		if err := rows.Scan(&value.ID, &value.Department, &value.Key, &value.Name, &value.Avatar, &value.Status, &value.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取加入申请失败：%w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *AuthDB) DepartmentRequest(ctx context.Context, id string) (DepartmentRequest, error) {
	var value DepartmentRequest
	err := s.db.QueryRowContext(ctx, `SELECT id,department,key,name,avatar,status,created_at FROM department_requests WHERE id=?`, id).Scan(&value.ID, &value.Department, &value.Key, &value.Name, &value.Avatar, &value.Status, &value.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrLANNotFound
	}
	return value, err
}

func (s *AuthDB) DecideDepartmentRequest(ctx context.Context, id, status string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE department_requests SET status=? WHERE id=? AND status=?`, status, id, LANPending)
	if err != nil {
		return fmt.Errorf("处理加入申请失败：%w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrLANNotFound
	}
	return nil
}
