package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/go-ldap/ldap/v3"
	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/rs/zerolog/log"
)

// Handler processes auth challenges by performing LDAP binds against the configured DC.
// It uses short-lived connections (one per auth attempt) to avoid interfering with the
// sync service account's persistent connection.
type Handler struct {
	ldapCfg config.LDAPConfig
}

func NewHandler(ldapCfg config.LDAPConfig) *Handler {
	return &Handler{ldapCfg: ldapCfg}
}

// HandleChallenge performs an LDAP simple bind with the user's credentials and returns the result.
func (h *Handler) HandleChallenge(ctx context.Context, challenge AuthChallenge) AuthResult {
	result := AuthResult{ChallengeID: challenge.ChallengeID}

	password := []byte(challenge.Password)
	defer zeroBytes(password)
	challenge.Password = ""

	userDN := challenge.BindDNHint
	if userDN == "" {
		var err error
		userDN, err = h.lookupUserDN(challenge.Personame)
		if err != nil {
			log.Error().Err(err).Str("personame", challenge.Personame).Msg("user DN lookup failed")
			result.Error = "user not found in directory"
			return result
		}
	}

	conn, err := h.dialLDAP()
	if err != nil {
		log.Error().Err(err).Msg("auth bind connection failed")
		result.Error = "directory connection failed"
		return result
	}
	defer conn.Close()

	err = conn.Bind(userDN, string(password))
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			result.Success = false
			result.UserDN = userDN
			return result
		}
		if ldap.IsErrorWithCode(err, ldap.LDAPResultConstraintViolation) {
			// AD returns this when password is expired
			result.Success = false
			result.Expired = true
			result.UserDN = userDN
			return result
		}
		log.Error().Err(err).Str("user_dn", userDN).Msg("LDAP bind error")
		result.Error = "directory error"
		return result
	}

	result.Success = true
	result.UserDN = userDN
	log.Info().Str("challenge_id", challenge.ChallengeID).Str("user_dn", userDN).Msg("auth bind succeeded")
	return result
}

// lookupUserDN searches for the user by sAMAccountName or cn using the service account.
// Tries AD-style filter first, falls back to posixAccount/cn for non-AD directories (e.g. glauth).
func (h *Handler) lookupUserDN(personame string) (string, error) {
	conn, err := h.dialAndBindService()
	if err != nil {
		return "", err
	}
	defer conn.Close()

	filters := []string{
		fmt.Sprintf("(&(objectClass=user)(objectCategory=person)(sAMAccountName=%s))", ldap.EscapeFilter(personame)),
		fmt.Sprintf("(&(objectClass=posixAccount)(cn=%s))", ldap.EscapeFilter(personame)),
	}

	for _, filter := range filters {
		searchReq := ldap.NewSearchRequest(
			h.ldapCfg.BaseDN,
			ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases,
			1, 10, false,
			filter,
			[]string{"dn"},
			nil,
		)

		result, err := conn.Search(searchReq)
		if err != nil {
			continue
		}
		if len(result.Entries) > 0 {
			return result.Entries[0].DN, nil
		}
	}

	return "", fmt.Errorf("no user found with personame=%s", personame)
}

func (h *Handler) dialLDAP() (*ldap.Conn, error) {
	address := fmt.Sprintf("%s:%d", h.ldapCfg.Host, h.ldapCfg.Port)
	tlsConfig, err := h.buildTLSConfig()
	if err != nil {
		return nil, err
	}

	var conn *ldap.Conn
	if h.ldapCfg.TLS {
		conn, err = ldap.DialTLS("tcp", address, tlsConfig)
	} else if h.ldapCfg.Plaintext {
		conn, err = ldap.Dial("tcp", address)
	} else {
		conn, err = ldap.Dial("tcp", address)
		if err == nil {
			err = conn.StartTLS(tlsConfig)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("LDAP dial: %w", err)
	}
	return conn, nil
}

func (h *Handler) dialAndBindService() (*ldap.Conn, error) {
	conn, err := h.dialLDAP()
	if err != nil {
		return nil, err
	}
	if err := conn.Bind(h.ldapCfg.BindDN, h.ldapCfg.BindPassword); err != nil {
		conn.Close()
		return nil, fmt.Errorf("service account bind: %w", err)
	}
	return conn, nil
}

func (h *Handler) buildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: h.ldapCfg.TLSSkipVerify,
		ServerName:         h.ldapCfg.Host,
	}

	if h.ldapCfg.CACert != "" {
		caCert, err := os.ReadFile(h.ldapCfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("parse CA cert failed")
		}
		tlsConfig.RootCAs = pool
	}

	return tlsConfig, nil
}

// zeroBytes overwrites a byte slice with zeros.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
