package sync

import (
	"time"

	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/rs/zerolog/log"
)

func (e *Engine) FullSync() (*Result, error) {
	result := &Result{}

	baseDN := e.userSearchBase()

	// Query all users matching filter
	entries, err := e.ldap.SearchUsers(baseDN, e.syncCfg.UserFilter, ldap.UserAttributes())
	if err != nil {
		e.store.SetLastError(err.Error())
		return nil, err
	}

	log.Info().Int("ad_users", len(entries)).Msg("full sync: queried all users")

	// Build set of current AD users by objectGUID
	adUserMap := make(map[string]*ldap.ADUser, len(entries))
	var maxUSN int64

	for _, entry := range entries {
		adUser := ldap.ExtractUser(entry, e.mapCfg.UserIDFormat)
		adUserMap[adUser.ObjectGUID] = adUser
		if adUser.USNChanged > maxUSN {
			maxUSN = adUser.USNChanged
		}
	}

	// Process all AD users (create or update)
	for _, adUser := range adUserMap {
		e.processUser(adUser, result)
	}

	// Detect deletions: users in state but not in AD
	stateUsers, err := e.store.GetAllUsers()
	if err != nil {
		log.Error().Err(err).Msg("failed to load state users for reconciliation")
	} else {
		for _, stateUser := range stateUsers {
			if _, exists := adUserMap[stateUser.ObjectGUID]; !exists {
				log.Info().Str("user", stateUser.Username).Msg("deleting user (not found in AD)")
				if !e.dryRun {
					if err := e.scim.DeleteUser(stateUser.Username); err != nil {
						log.Error().Err(err).Str("user", stateUser.Username).Msg("SCIM delete failed")
						result.Errors++
						continue
					}
					e.store.DeleteUser(stateUser.ObjectGUID)
				}
				result.Deleted++
			}
		}
	}

	// Sync groups and membership
	groupsCreated, _, groupErr := e.SyncGroups()
	if groupErr != nil {
		log.Error().Err(groupErr).Msg("group sync failed")
	} else if groupsCreated > 0 {
		log.Info().Int("groups_created", groupsCreated).Msg("groups synced")
	}
	e.SyncMembership(result)

	// Update high-water mark to current
	if maxUSN > 0 && !e.dryRun {
		if err := e.store.SetHighWaterMark(maxUSN); err != nil {
			log.Error().Err(err).Msg("failed to update high-water mark")
		}
	}

	if !e.dryRun {
		e.store.SetLastSync(time.Now())
		e.store.SetLastFullSync(time.Now())
		e.store.SetLastError("")
	}

	return result, nil
}
