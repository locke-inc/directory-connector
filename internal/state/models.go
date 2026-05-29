package state

import "time"

type SyncedUser struct {
	ObjectGUID string
	Username   string
	Email      string
	FirstName  string
	LastName   string
	Disabled   bool
	MemberOf   string // JSON array of group DNs
	UpdatedAt  time.Time
}

type SyncedGroup struct {
	ObjectGUID string
	CN         string
	Members    string // JSON array of member DNs
	UpdatedAt  time.Time
}

type SyncInfo struct {
	LastSync       time.Time
	LastFullSync   time.Time
	HighWaterMark  int64
	UserCount      int
	GroupCount     int
	LastError      string
}
