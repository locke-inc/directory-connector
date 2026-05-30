package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("failed to open state database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) GetUser(objectGUID string) (*SyncedUser, error) {
	row := s.db.QueryRow(
		"SELECT object_guid, username, email, first_name, last_name, disabled, member_of, updated_at FROM users WHERE object_guid = ?",
		objectGUID,
	)

	u := &SyncedUser{}
	var disabled int
	err := row.Scan(&u.ObjectGUID, &u.Username, &u.Email, &u.FirstName, &u.LastName, &disabled, &u.MemberOf, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Disabled = disabled != 0
	return u, nil
}

func (s *Store) UpsertUser(u *SyncedUser) error {
	disabled := 0
	if u.Disabled {
		disabled = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO users (object_guid, username, email, first_name, last_name, disabled, member_of, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(object_guid) DO UPDATE SET
		   username=excluded.username, email=excluded.email,
		   first_name=excluded.first_name, last_name=excluded.last_name,
		   disabled=excluded.disabled, member_of=excluded.member_of,
		   updated_at=excluded.updated_at`,
		u.ObjectGUID, u.Username, u.Email, u.FirstName, u.LastName, disabled, u.MemberOf, time.Now(),
	)
	return err
}

func (s *Store) DeleteUser(objectGUID string) error {
	_, err := s.db.Exec("DELETE FROM users WHERE object_guid = ?", objectGUID)
	return err
}

func (s *Store) GetAllUsers() ([]*SyncedUser, error) {
	rows, err := s.db.Query("SELECT object_guid, username, email, first_name, last_name, disabled, member_of, updated_at FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*SyncedUser
	for rows.Next() {
		u := &SyncedUser{}
		var disabled int
		if err := rows.Scan(&u.ObjectGUID, &u.Username, &u.Email, &u.FirstName, &u.LastName, &disabled, &u.MemberOf, &u.UpdatedAt); err != nil {
			return nil, err
		}
		u.Disabled = disabled != 0
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) UpsertGroup(g *SyncedGroup) error {
	_, err := s.db.Exec(
		`INSERT INTO groups (object_guid, cn, members, scim_group_id, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(object_guid) DO UPDATE SET
		   cn=excluded.cn, members=excluded.members, scim_group_id=excluded.scim_group_id, updated_at=excluded.updated_at`,
		g.ObjectGUID, g.CN, g.Members, g.SCIMGroupID, time.Now(),
	)
	return err
}

func (s *Store) GetGroup(objectGUID string) (*SyncedGroup, error) {
	row := s.db.QueryRow(
		"SELECT object_guid, cn, members, scim_group_id, updated_at FROM groups WHERE object_guid = ?",
		objectGUID,
	)
	g := &SyncedGroup{}
	err := row.Scan(&g.ObjectGUID, &g.CN, &g.Members, &g.SCIMGroupID, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Store) DeleteGroup(objectGUID string) error {
	_, err := s.db.Exec("DELETE FROM groups WHERE object_guid = ?", objectGUID)
	return err
}

func (s *Store) GetAllGroups() ([]*SyncedGroup, error) {
	rows, err := s.db.Query("SELECT object_guid, cn, members, scim_group_id, updated_at FROM groups")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*SyncedGroup
	for rows.Next() {
		g := &SyncedGroup{}
		if err := rows.Scan(&g.ObjectGUID, &g.CN, &g.Members, &g.SCIMGroupID, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Store) GetHighWaterMark() (int64, error) {
	return s.getInt64("high_water_mark")
}

func (s *Store) SetHighWaterMark(usn int64) error {
	return s.setString("high_water_mark", strconv.FormatInt(usn, 10))
}

func (s *Store) GetSyncInfo() (*SyncInfo, error) {
	info := &SyncInfo{}

	if t, err := s.getTime("last_sync"); err == nil {
		info.LastSync = t
	}
	if t, err := s.getTime("last_full_sync"); err == nil {
		info.LastFullSync = t
	}
	info.HighWaterMark, _ = s.GetHighWaterMark()
	info.LastError, _ = s.getString("last_error")

	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	info.UserCount = count

	s.db.QueryRow("SELECT COUNT(*) FROM groups").Scan(&count)
	info.GroupCount = count

	return info, nil
}

func (s *Store) SetLastSync(t time.Time) error {
	return s.setString("last_sync", t.Format(time.RFC3339))
}

func (s *Store) SetLastFullSync(t time.Time) error {
	return s.setString("last_full_sync", t.Format(time.RFC3339))
}

func (s *Store) SetLastError(errMsg string) error {
	return s.setString("last_error", errMsg)
}

func (s *Store) getInt64(key string) (int64, error) {
	val, err := s.getString(key)
	if err != nil || val == "" {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

func (s *Store) getString(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM sync_state WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (s *Store) setString(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO sync_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value,
	)
	return err
}

func (s *Store) getTime(key string) (time.Time, error) {
	val, err := s.getString(key)
	if err != nil || val == "" {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, val)
}

// MemberOfJSON converts a slice of group DNs to JSON for storage.
func MemberOfJSON(groups []string) string {
	b, _ := json.Marshal(groups)
	return string(b)
}

// ParseMemberOf parses the stored JSON group membership.
func ParseMemberOf(jsonStr string) ([]string, error) {
	if jsonStr == "" || jsonStr == "null" {
		return nil, nil
	}
	var groups []string
	if err := json.Unmarshal([]byte(jsonStr), &groups); err != nil {
		return nil, fmt.Errorf("failed to parse member_of JSON: %w", err)
	}
	return groups, nil
}
