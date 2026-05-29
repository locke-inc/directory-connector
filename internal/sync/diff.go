package sync

import (
	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/locke-inc/directory-connector/internal/state"
)

func userAttributesChanged(existing *state.SyncedUser, adUser *ldap.ADUser) bool {
	if existing.Username != adUser.SAMAccountName {
		return true
	}
	if existing.Email != adUser.Email {
		return true
	}
	if existing.FirstName != adUser.FirstName {
		return true
	}
	if existing.LastName != adUser.LastName {
		return true
	}
	return false
}
