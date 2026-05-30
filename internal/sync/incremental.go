package sync

import (
	"time"

	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/rs/zerolog/log"
)

func (e *Engine) IncrementalSync() (*Result, error) {
	result := &Result{}

	highWaterMark, err := e.store.GetHighWaterMark()
	if err != nil {
		return nil, err
	}

	// If no high-water mark, fall back to full sync
	if highWaterMark == 0 {
		log.Info().Msg("no high-water mark found, falling back to full sync")
		return e.FullSync()
	}

	baseDN := e.userSearchBase()

	// Query changed users
	entries, err := e.ldap.SearchChangedUsers(baseDN, e.syncCfg.UserFilter, ldap.UserAttributes(), highWaterMark)
	if err != nil {
		e.store.SetLastError(err.Error())
		return nil, err
	}

	log.Debug().Int("changed_users", len(entries)).Int64("since_usn", highWaterMark).Msg("incremental query complete")

	var maxUSN int64 = highWaterMark
	for _, entry := range entries {
		adUser := ldap.ExtractUser(entry, e.mapCfg.UserIDFormat)
		e.processUser(adUser, result)
		if adUser.USNChanged > maxUSN {
			maxUSN = adUser.USNChanged
		}
	}

	// Detect deletes via tombstones
	tombstones, err := e.ldap.SearchDeletedObjects(baseDN, highWaterMark)
	if err != nil {
		log.Warn().Err(err).Msg("tombstone search failed (delete detection degraded)")
	} else {
		for _, entry := range tombstones {
			guidBytes := entry.GetRawAttributeValue("objectGUID")
			if len(guidBytes) != 16 {
				continue
			}
			objectGUID := ldap.FormatObjectGUID(guidBytes, e.mapCfg.UserIDFormat)
			existing, err := e.store.GetUser(objectGUID)
			if err != nil || existing == nil {
				continue
			}

			log.Info().Str("user", existing.Username).Msg("deleting user (tombstone detected)")
			if !e.dryRun {
				if err := e.scim.DeleteUser(existing.Username); err != nil {
					log.Error().Err(err).Str("user", existing.Username).Msg("SCIM delete failed")
					result.Errors++
					continue
				}
				e.store.DeleteUser(objectGUID)
			}
			result.Deleted++
		}
	}

	// Sync membership changes (based on updated user memberOf)
	if len(entries) > 0 {
		e.SyncMembership(result)
	}

	// Update high-water mark
	if maxUSN > highWaterMark && !e.dryRun {
		if err := e.store.SetHighWaterMark(maxUSN); err != nil {
			log.Error().Err(err).Msg("failed to update high-water mark")
		}
	}

	if !e.dryRun {
		e.store.SetLastSync(time.Now())
		e.store.SetLastError("")
	}

	return result, nil
}
