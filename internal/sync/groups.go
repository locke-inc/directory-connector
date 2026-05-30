package sync

import (
	"path/filepath"
	"strings"

	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/locke-inc/directory-connector/internal/scim"
	"github.com/locke-inc/directory-connector/internal/state"
	"github.com/rs/zerolog/log"
)

func (e *Engine) SyncGroups() (created, updated int, err error) {
	baseDN := e.groupSearchBase()

	entries, err := e.ldap.SearchGroups(baseDN, e.syncCfg.GroupFilter)
	if err != nil {
		return 0, 0, err
	}

	for _, entry := range entries {
		adGroup := ldap.ExtractGroup(entry, e.mapCfg.UserIDFormat)

		if !e.groupIncluded(adGroup) {
			continue
		}

		existing, _ := e.store.GetGroup(adGroup.ObjectGUID)

		if existing == nil {
			log.Info().Str("group", adGroup.CN).Msg("syncing new group to SCIM")
			if !e.dryRun {
				scimGroup, scimErr := e.scim.CreateGroup(&scim.SCIMGroup{
					DisplayName: adGroup.CN,
					ExternalID:  adGroup.ObjectGUID,
				})
				if scimErr != nil {
					log.Error().Err(scimErr).Str("group", adGroup.CN).Msg("SCIM group create failed")
					continue
				}
				e.store.UpsertGroup(&state.SyncedGroup{
					ObjectGUID: adGroup.ObjectGUID,
					CN:         adGroup.CN,
					Members:    state.MemberOfJSON(adGroup.Members),
					SCIMGroupID: scimGroup.ID,
				})
			}
			created++
		} else if existing.CN != adGroup.CN {
			log.Info().Str("group", adGroup.CN).Msg("group renamed")
			if !e.dryRun {
				e.store.UpsertGroup(&state.SyncedGroup{
					ObjectGUID:  adGroup.ObjectGUID,
					CN:          adGroup.CN,
					Members:     state.MemberOfJSON(adGroup.Members),
					SCIMGroupID: existing.SCIMGroupID,
				})
			}
			updated++
		}
	}

	return created, updated, nil
}

func (e *Engine) SyncMembership(result *Result) {
	groups, err := e.store.GetAllGroups()
	if err != nil {
		log.Error().Err(err).Msg("failed to load groups for membership sync")
		return
	}

	allUsers, err := e.store.GetAllUsers()
	if err != nil {
		log.Error().Err(err).Msg("failed to load users for membership sync")
		return
	}

	// Build DN → username lookup
	userByGUID := make(map[string]*state.SyncedUser, len(allUsers))
	for _, u := range allUsers {
		userByGUID[u.ObjectGUID] = u
	}

	for _, group := range groups {
		if group.SCIMGroupID == "" {
			continue
		}

		currentMembers, _ := state.ParseMemberOf(group.Members)
		currentSet := make(map[string]bool, len(currentMembers))
		for _, m := range currentMembers {
			currentSet[m] = true
		}

		// Get fresh membership from AD group state
		// We need to look up actual AD members for this group
		// The stored Members field has the latest from the last sync
		// Compare against user memberOf attributes to detect changes
		for _, user := range allUsers {
			userGroups, _ := state.ParseMemberOf(user.MemberOf)
			isMember := false
			for _, g := range userGroups {
				if dnMatchesGroup(g, group.CN) {
					isMember = true
					break
				}
			}

			wasKnownMember := currentSet[user.Username]

			if isMember && !wasKnownMember {
				log.Info().Str("user", user.Username).Str("group", group.CN).Msg("adding user to group")
				if !e.dryRun {
					if err := e.scim.AddGroupMember(group.SCIMGroupID, user.Username); err != nil {
						log.Error().Err(err).Str("user", user.Username).Str("group", group.CN).Msg("SCIM add member failed")
						result.Errors++
					}
				}
			} else if !isMember && wasKnownMember {
				log.Info().Str("user", user.Username).Str("group", group.CN).Msg("removing user from group")
				if !e.dryRun {
					if err := e.scim.RemoveGroupMember(group.SCIMGroupID, user.Username); err != nil {
						log.Error().Err(err).Str("user", user.Username).Str("group", group.CN).Msg("SCIM remove member failed")
						result.Errors++
					}
				}
			}
		}

		// Update stored membership to current state
		if !e.dryRun {
			var memberUsernames []string
			for _, user := range allUsers {
				userGroups, _ := state.ParseMemberOf(user.MemberOf)
				for _, g := range userGroups {
					if dnMatchesGroup(g, group.CN) {
						memberUsernames = append(memberUsernames, user.Username)
						break
					}
				}
			}
			e.store.UpsertGroup(&state.SyncedGroup{
				ObjectGUID:  group.ObjectGUID,
				CN:          group.CN,
				Members:     state.MemberOfJSON(memberUsernames),
				SCIMGroupID: group.SCIMGroupID,
			})
		}
	}
}

func (e *Engine) groupIncluded(group *ldap.ADGroup) bool {
	if len(e.syncCfg.GroupExclude) > 0 {
		for _, pattern := range e.syncCfg.GroupExclude {
			if matchDNPattern(group.DistinguishedName, pattern) {
				return false
			}
		}
	}

	if len(e.syncCfg.GroupInclude) > 0 {
		for _, pattern := range e.syncCfg.GroupInclude {
			if matchDNPattern(group.DistinguishedName, pattern) {
				return true
			}
		}
		return false
	}

	return true
}

func matchDNPattern(dn, pattern string) bool {
	if strings.Contains(pattern, "*") {
		matched, _ := filepath.Match(pattern, dn)
		return matched
	}
	return strings.EqualFold(dn, pattern)
}

func dnMatchesGroup(dn, cn string) bool {
	return strings.HasPrefix(strings.ToLower(dn), "cn="+strings.ToLower(cn)+",")
}
