package state

const schema = `
CREATE TABLE IF NOT EXISTS users (
	object_guid TEXT PRIMARY KEY,
	username TEXT NOT NULL,
	email TEXT,
	first_name TEXT,
	last_name TEXT,
	disabled INTEGER DEFAULT 0,
	member_of TEXT DEFAULT '[]',
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS groups (
	object_guid TEXT PRIMARY KEY,
	cn TEXT NOT NULL,
	members TEXT DEFAULT '[]',
	scim_group_id TEXT DEFAULT '',
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sync_state (
	key TEXT PRIMARY KEY,
	value TEXT
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
`
