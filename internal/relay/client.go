package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/rs/zerolog/log"
)

type AuthChallenge struct {
	ChallengeID string `json:"challenge_id"`
	Personame   string `json:"personame"`
	Password    string `json:"password"`
	BindDNHint  string `json:"bind_dn_hint"`
}

type AuthResult struct {
	ChallengeID string `json:"challenge_id"`
	Success     bool   `json:"success"`
	UserDN      string `json:"user_dn,omitempty"`
	Expired     bool   `json:"expired,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ChallengeHandler func(ctx context.Context, challenge AuthChallenge) AuthResult

type Client struct {
	streamURL   string
	resultURL   string
	token       string
	httpClient  *http.Client
	handler     ChallengeHandler
	lastEventID string
	mu          sync.Mutex
}

func NewClient(cfg config.RelayConfig, token string, handler ChallengeHandler) *Client {
	return &Client{
		streamURL:  cfg.StreamEndpoint,
		resultURL:  cfg.ResultEndpoint,
		token:      token,
		httpClient: &http.Client{Timeout: 0}, // no timeout — SSE is long-lived
		handler:    handler,
	}
}

// Run connects to the SSE stream and processes challenges until ctx is cancelled.
// It automatically reconnects on failure with exponential backoff.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.connectAndProcess(ctx)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			log.Warn().Err(err).Dur("backoff", backoff).Msg("auth relay stream disconnected, reconnecting")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) connectAndProcess(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.streamURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	c.mu.Lock()
	if c.lastEventID != "" {
		req.Header.Set("Last-Event-ID", c.lastEventID)
	}
	c.mu.Unlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	log.Info().Str("url", c.streamURL).Msg("auth relay stream connected")

	// Reset backoff on successful connection (caller handles this via return nil path)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	heartbeatTimer := time.NewTimer(90 * time.Second)
	defer heartbeatTimer.Stop()

	// Process SSE events line by line
	var eventType string
	var dataLines []string
	var eventID string

	lineCh := make(chan string)
	errCh := make(chan error, 1)

	go func() {
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- fmt.Errorf("stream closed")
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case <-heartbeatTimer.C:
			return fmt.Errorf("heartbeat timeout (90s without data)")
		case line := <-lineCh:
			heartbeatTimer.Reset(90 * time.Second)

			if line == "" {
				// Empty line = end of event
				if eventType == "auth_challenge" && len(dataLines) > 0 {
					c.handleEvent(ctx, eventID, strings.Join(dataLines, "\n"))
				}
				eventType = ""
				dataLines = nil
				eventID = ""
				continue
			}

			if strings.HasPrefix(line, ":") {
				// Comment (keepalive)
				continue
			}

			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
			} else if strings.HasPrefix(line, "id:") {
				eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
		}
	}
}

func (c *Client) handleEvent(ctx context.Context, eventID, data string) {
	if eventID != "" {
		c.mu.Lock()
		c.lastEventID = eventID
		c.mu.Unlock()
	}

	var challenge AuthChallenge
	if err := json.Unmarshal([]byte(data), &challenge); err != nil {
		log.Error().Err(err).Str("data", data).Msg("failed to parse auth challenge")
		return
	}

	log.Info().Str("challenge_id", challenge.ChallengeID).Str("personame", challenge.Personame).Msg("received auth challenge")

	// Process in a goroutine so we don't block the stream
	go func() {
		result := c.handler(ctx, challenge)
		if err := c.postResult(ctx, result); err != nil {
			log.Error().Err(err).Str("challenge_id", challenge.ChallengeID).Msg("failed to post auth result")
		}
	}()
}

func (c *Client) postResult(ctx context.Context, result AuthResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.resultURL, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("post result: status %d", resp.StatusCode)
	}

	log.Debug().Str("challenge_id", result.ChallengeID).Bool("success", result.Success).Msg("posted auth result")
	return nil
}
