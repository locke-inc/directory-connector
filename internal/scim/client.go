package scim

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/rs/zerolog/log"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	rateLimiter *rateLimiter
}

func NewClient(cfg config.LockeConfig) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimRight(cfg.APIURL, "/") + "/scim/v2",
		token:      cfg.SCIMToken,
		rateLimiter: newRateLimiter(80, time.Minute),
	}
}

func (c *Client) CreateUser(user *SCIMUser) error {
	user.Schemas = []string{SchemaUser}
	err := c.doRequest("POST", "/Users", user, nil)
	if err != nil {
		var scimErr *SCIMError
		if errors.As(err, &scimErr) && scimErr.IsConflict() {
			return fmt.Errorf("%w: %s", ErrConflict, scimErr.Body)
		}
	}
	return err
}

func (c *Client) UpdateUser(username string, user *SCIMUser) error {
	return c.doRequest("PUT", "/Users/"+username, user, nil)
}

func (c *Client) PatchUserActive(username string, active bool) error {
	patch := &SCIMPatchOp{
		Schemas: []string{SchemaPatchOp},
		Operations: []SCIMOperation{
			{Op: "replace", Path: "active", Value: active},
		},
	}
	return c.doRequest("PATCH", "/Users/"+username, patch, nil)
}

func (c *Client) DeleteUser(username string) error {
	return c.doRequest("DELETE", "/Users/"+username, nil, nil)
}

func (c *Client) CreateGroup(group *SCIMGroup) (*SCIMGroup, error) {
	group.Schemas = []string{SchemaGroup}
	var result SCIMGroup
	if err := c.doRequest("POST", "/Groups", group, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListGroups() ([]SCIMGroup, error) {
	var result SCIMListResponse
	if err := c.doRequest("GET", "/Groups", nil, &result); err != nil {
		return nil, err
	}
	return result.Resources, nil
}

func (c *Client) AddGroupMember(groupID, username string) error {
	patch := &SCIMPatchOp{
		Schemas: []string{SchemaPatchOp},
		Operations: []SCIMOperation{
			{
				Op:   "add",
				Path: "members",
				Value: []SCIMGroupMember{{Value: username}},
			},
		},
	}
	return c.doRequest("PATCH", "/Groups/"+groupID, patch, nil)
}

func (c *Client) RemoveGroupMember(groupID, username string) error {
	patch := &SCIMPatchOp{
		Schemas: []string{SchemaPatchOp},
		Operations: []SCIMOperation{
			{
				Op:   "remove",
				Path: "members",
				Value: []SCIMGroupMember{{Value: username}},
			},
		},
	}
	return c.doRequest("PATCH", "/Groups/"+groupID, patch, nil)
}

type SyncReport struct {
	Created      int              `json:"created"`
	Updated      int              `json:"updated"`
	Disabled     int              `json:"disabled"`
	Deleted      int              `json:"deleted"`
	Skipped      int              `json:"skipped"`
	Errors       int              `json:"errors"`
	SkippedUsers []SyncReportUser `json:"skipped_users,omitempty"`
}

type SyncReportUser struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Reason   string `json:"reason"`
}

func (c *Client) ReportSyncResult(report *SyncReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal sync report: %w", err)
	}

	// POST to /connector/sync-report (not under /scim/v2)
	baseURL := strings.TrimSuffix(c.baseURL, "/scim/v2")
	url := baseURL + "/connector/sync-report"

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create sync report request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post sync report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("sync report: status %d", resp.StatusCode)
	}

	return nil
}

const maxResponseBytes = 4 * 1024 * 1024 // 4MB max response size

func (c *Client) doRequest(method, path string, body interface{}, result interface{}) error {
	c.rateLimiter.Wait()

	url := c.baseURL + path

	var jsonBytes []byte
	if body != nil {
		var err error
		jsonBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	const maxAttempts = 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var bodyReader io.Reader
		if jsonBytes != nil {
			bodyReader = bytes.NewReader(jsonBytes)
		}

		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/scim+json")
		req.Header.Set("Accept", "application/scim+json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < 4 {
				backoff := time.Duration(1<<uint(attempt)) * time.Second
				log.Warn().Err(err).Dur("backoff", backoff).Int("attempt", attempt+1).Msg("network error, retrying")
				time.Sleep(backoff)
				continue
			}
			return fmt.Errorf("request failed after retries: %w", err)
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()

		if resp.StatusCode == 429 {
			retryAfter := 60 * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if seconds, err := strconv.Atoi(ra); err == nil {
					retryAfter = time.Duration(seconds) * time.Second
				}
			}
			backoff := retryAfter * time.Duration(1<<uint(attempt))
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			log.Warn().Dur("backoff", backoff).Int("attempt", attempt+1).Msg("rate limited, waiting")
			time.Sleep(backoff)
			continue
		}

		if resp.StatusCode >= 500 && attempt < 3 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			log.Warn().Int("status", resp.StatusCode).Dur("backoff", backoff).Int("attempt", attempt+1).Msg("server error, retrying")
			time.Sleep(backoff)
			continue
		}

		if resp.StatusCode >= 400 {
			return &SCIMError{StatusCode: resp.StatusCode, Body: string(respBody)}
		}

		if result != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}
		}

		return nil
	}

	return fmt.Errorf("request failed: exceeded maximum retry attempts")
}
