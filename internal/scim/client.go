package scim

import (
	"bytes"
	"encoding/json"
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
	return c.doRequest("POST", "/Users", user, nil)
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

func (c *Client) doRequest(method, path string, body interface{}, result interface{}) error {
	c.rateLimiter.Wait()

	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBytes)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/scim+json")
	req.Header.Set("Accept", "application/scim+json")

	var resp *http.Response
	for attempt := 0; attempt < 5; attempt++ {
		resp, err = c.httpClient.Do(req)
		if err != nil {
			if attempt < 4 {
				backoff := time.Duration(1<<uint(attempt)) * time.Second
				log.Warn().Err(err).Dur("backoff", backoff).Int("attempt", attempt+1).Msg("network error, retrying")
				time.Sleep(backoff)
				continue
			}
			return fmt.Errorf("request failed after retries: %w", err)
		}
		break
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 429 {
		return c.handleRateLimit(resp, method, path, body, result)
	}

	if resp.StatusCode >= 500 {
		return c.handleServerError(resp, respBody, method, path, body, result)
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

func (c *Client) handleRateLimit(resp *http.Response, method, path string, body, result interface{}) error {
	retryAfter := 60 * time.Second
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if seconds, err := strconv.Atoi(ra); err == nil {
			retryAfter = time.Duration(seconds) * time.Second
		}
	}

	for attempt := 0; attempt < 5; attempt++ {
		backoff := retryAfter * time.Duration(1<<uint(attempt))
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		log.Warn().Dur("backoff", backoff).Msg("rate limited, waiting")
		time.Sleep(backoff)

		err := c.doRequest(method, path, body, result)
		if err == nil {
			return nil
		}
		if scimErr, ok := err.(*SCIMError); ok && scimErr.StatusCode == 429 {
			continue
		}
		return err
	}
	return fmt.Errorf("rate limited: exceeded retry attempts")
}

func (c *Client) handleServerError(resp *http.Response, respBody []byte, method, path string, body, result interface{}) error {
	for attempt := 0; attempt < 3; attempt++ {
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		log.Warn().Int("status", resp.StatusCode).Dur("backoff", backoff).Msg("server error, retrying")
		time.Sleep(backoff)

		err := c.doRequest(method, path, body, result)
		if err == nil {
			return nil
		}
		if scimErr, ok := err.(*SCIMError); ok && scimErr.StatusCode >= 500 {
			continue
		}
		return err
	}
	return &SCIMError{StatusCode: resp.StatusCode, Body: string(respBody)}
}
