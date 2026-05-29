package sync

import (
	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/locke-inc/directory-connector/internal/scim"
	"github.com/locke-inc/directory-connector/internal/state"
	"github.com/rs/zerolog/log"
)

type Result struct {
	Created  int
	Updated  int
	Disabled int
	Deleted  int
	Errors   int
}

type Engine struct {
	ldap    *ldap.Client
	scim    *scim.Client
	store   *state.Store
	syncCfg config.SyncConfig
	mapCfg  config.MappingConfig
	dryRun  bool
}

func NewEngine(ldapClient *ldap.Client, scimClient *scim.Client, store *state.Store, syncCfg config.SyncConfig, mapCfg config.MappingConfig, dryRun bool) *Engine {
	return &Engine{
		ldap:    ldapClient,
		scim:    scimClient,
		store:   store,
		syncCfg: syncCfg,
		mapCfg:  mapCfg,
		dryRun:  dryRun,
	}
}

func (e *Engine) userSearchBase() string {
	if e.syncCfg.UserSearchBase != "" {
		return e.syncCfg.UserSearchBase
	}
	return e.ldap.BaseDN()
}

func (e *Engine) groupSearchBase() string {
	if e.syncCfg.GroupSearchBase != "" {
		return e.syncCfg.GroupSearchBase
	}
	return e.ldap.BaseDN()
}

func (e *Engine) adUserToSCIM(user *ldap.ADUser) *scim.SCIMUser {
	return &scim.SCIMUser{
		UserName:   user.SAMAccountName,
		ExternalID: user.ObjectGUID,
		Name: scim.SCIMName{
			GivenName:  user.FirstName,
			FamilyName: user.LastName,
		},
		Emails: []scim.SCIMEmail{
			{Value: user.Email, Primary: true},
		},
		Active: !user.Disabled,
	}
}

func (e *Engine) adUserToState(user *ldap.ADUser) *state.SyncedUser {
	return &state.SyncedUser{
		ObjectGUID: user.ObjectGUID,
		Username:   user.SAMAccountName,
		Email:      user.Email,
		FirstName:  user.FirstName,
		LastName:   user.LastName,
		Disabled:   user.Disabled,
		MemberOf:   state.MemberOfJSON(user.MemberOf),
	}
}

func (e *Engine) processUser(adUser *ldap.ADUser, result *Result) {
	existing, err := e.store.GetUser(adUser.ObjectGUID)
	if err != nil {
		log.Error().Err(err).Str("user", adUser.SAMAccountName).Msg("failed to look up user in state")
		result.Errors++
		return
	}

	if existing == nil {
		// New user — create
		log.Info().Str("user", adUser.SAMAccountName).Msg("creating user")
		if !e.dryRun {
			if err := e.scim.CreateUser(e.adUserToSCIM(adUser)); err != nil {
				log.Error().Err(err).Str("user", adUser.SAMAccountName).Msg("SCIM create failed")
				result.Errors++
				return
			}
			if err := e.store.UpsertUser(e.adUserToState(adUser)); err != nil {
				log.Error().Err(err).Str("user", adUser.SAMAccountName).Msg("state upsert failed")
				result.Errors++
				return
			}
		}
		result.Created++
		return
	}

	// Existing user — check for changes
	changed := false

	if adUser.Disabled && !existing.Disabled {
		log.Info().Str("user", adUser.SAMAccountName).Msg("disabling user")
		if !e.dryRun {
			if err := e.scim.PatchUserActive(existing.Username, false); err != nil {
				log.Error().Err(err).Str("user", adUser.SAMAccountName).Msg("SCIM disable failed")
				result.Errors++
				return
			}
		}
		result.Disabled++
		changed = true
	} else if !adUser.Disabled && existing.Disabled {
		log.Info().Str("user", adUser.SAMAccountName).Msg("re-enabling user")
		if !e.dryRun {
			if err := e.scim.PatchUserActive(existing.Username, true); err != nil {
				log.Error().Err(err).Str("user", adUser.SAMAccountName).Msg("SCIM re-enable failed")
				result.Errors++
				return
			}
		}
		changed = true
	}

	if userAttributesChanged(existing, adUser) {
		log.Info().Str("user", adUser.SAMAccountName).Msg("updating user attributes")
		if !e.dryRun {
			if err := e.scim.UpdateUser(existing.Username, e.adUserToSCIM(adUser)); err != nil {
				log.Error().Err(err).Str("user", adUser.SAMAccountName).Msg("SCIM update failed")
				result.Errors++
				return
			}
		}
		if !changed {
			result.Updated++
		}
		changed = true
	}

	if changed && !e.dryRun {
		if err := e.store.UpsertUser(e.adUserToState(adUser)); err != nil {
			log.Error().Err(err).Str("user", adUser.SAMAccountName).Msg("state update failed")
		}
	}
}
