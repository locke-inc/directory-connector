package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/locke-inc/directory-connector/internal/config"
)

func TestClient_ProcessesAuthChallenge(t *testing.T) {
	challenge := AuthChallenge{
		ChallengeID: "test-123",
		Personame:   "jsmith",
		Password:    "s3cret",
		BindDNHint:  "CN=John Smith,OU=Users,DC=test,DC=local",
	}

	var receivedResult AuthResult
	var resultMu sync.Mutex

	// Mock result endpoint
	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing or wrong auth header on result POST")
			w.WriteHeader(401)
			return
		}
		resultMu.Lock()
		defer resultMu.Unlock()
		json.NewDecoder(r.Body).Decode(&receivedResult)
		w.WriteHeader(200)
	}))
	defer resultServer.Close()

	// Mock SSE stream
	streamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing or wrong auth header on stream")
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Error("missing Accept: text/event-stream header")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		flusher := w.(http.Flusher)

		data, _ := json.Marshal(challenge)
		fmt.Fprintf(w, "id:evt-1\nevent:auth_challenge\ndata:%s\n\n", data)
		flusher.Flush()

		// Keep connection open briefly for the client to process
		time.Sleep(500 * time.Millisecond)
	}))
	defer streamServer.Close()

	// Handler that always succeeds
	handler := func(ctx context.Context, c AuthChallenge) AuthResult {
		return AuthResult{
			ChallengeID: c.ChallengeID,
			Success:     true,
			UserDN:      c.BindDNHint,
		}
	}

	cfg := config.RelayConfig{
		Enabled:        true,
		StreamEndpoint: streamServer.URL,
		ResultEndpoint: resultServer.URL,
	}

	client := NewClient(cfg, "test-token", handler)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client.Run(ctx)

	resultMu.Lock()
	defer resultMu.Unlock()

	if receivedResult.ChallengeID != "test-123" {
		t.Errorf("expected challenge_id=test-123, got %s", receivedResult.ChallengeID)
	}
	if !receivedResult.Success {
		t.Error("expected success=true")
	}
	if receivedResult.UserDN != "CN=John Smith,OU=Users,DC=test,DC=local" {
		t.Errorf("unexpected user_dn: %s", receivedResult.UserDN)
	}
}

func TestClient_ReconnectsOnStreamClose(t *testing.T) {
	connectCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connectCount++
		count := connectCount
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		flusher := w.(http.Flusher)

		// First connection: send keepalive then close
		if count <= 2 {
			fmt.Fprintf(w, ":keepalive\n\n")
			flusher.Flush()
			time.Sleep(50 * time.Millisecond)
			return // close connection
		}

		// Third connection: stay open until context cancelled
		<-r.Context().Done()
	}))
	defer server.Close()

	handler := func(ctx context.Context, c AuthChallenge) AuthResult {
		return AuthResult{ChallengeID: c.ChallengeID, Success: true}
	}

	cfg := config.RelayConfig{
		Enabled:        true,
		StreamEndpoint: server.URL,
		ResultEndpoint: server.URL + "/result",
	}

	client := NewClient(cfg, "test-token", handler)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if connectCount < 2 {
		t.Errorf("expected at least 2 connection attempts, got %d", connectCount)
	}
}

func TestClient_LastEventIDSentOnReconnect(t *testing.T) {
	var lastEventIDs []string
	var mu sync.Mutex
	connectCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastEventIDs = append(lastEventIDs, r.Header.Get("Last-Event-ID"))
		connectCount++
		count := connectCount
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		if count == 1 {
			// Send an event with an ID, then close
			fmt.Fprintf(w, "id:evt-42\nevent:auth_challenge\ndata:{\"challenge_id\":\"c1\",\"personame\":\"x\",\"password\":\"p\",\"bind_dn_hint\":\"\"}\n\n")
			flusher.Flush()
			time.Sleep(100 * time.Millisecond)
			return
		}

		<-r.Context().Done()
	}))
	defer server.Close()

	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer resultServer.Close()

	handler := func(ctx context.Context, c AuthChallenge) AuthResult {
		return AuthResult{ChallengeID: c.ChallengeID, Success: true}
	}

	cfg := config.RelayConfig{
		Enabled:        true,
		StreamEndpoint: server.URL,
		ResultEndpoint: resultServer.URL,
	}

	client := NewClient(cfg, "test-token", handler)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client.Run(ctx)

	mu.Lock()
	defer mu.Unlock()

	if len(lastEventIDs) < 2 {
		t.Fatalf("expected at least 2 connections, got %d", len(lastEventIDs))
	}
	if lastEventIDs[0] != "" {
		t.Errorf("first connection should have empty Last-Event-ID, got %q", lastEventIDs[0])
	}
	if lastEventIDs[1] != "evt-42" {
		t.Errorf("second connection should have Last-Event-ID=evt-42, got %q", lastEventIDs[1])
	}
}

func TestZeroBytes(t *testing.T) {
	b := []byte("secret-password")
	zeroBytes(b)

	for i, v := range b {
		if v != 0 {
			t.Errorf("byte %d not zeroed: %d", i, v)
		}
	}
}

func TestHandler_InvalidCredentials(t *testing.T) {
	// This test validates the handler's response structure for failed auth.
	// We can't easily mock the LDAP bind without a test LDAP server,
	// so we test the handler with a non-existent host to verify error path.
	h := NewHandler(config.LDAPConfig{
		Host:      "localhost",
		Port:      0, // invalid port — connection will fail
		TLS:       false,
		Plaintext: true,
		BaseDN:    "DC=test,DC=local",
	})

	ctx := context.Background()
	result := h.HandleChallenge(ctx, AuthChallenge{
		ChallengeID: "fail-test",
		Personame:   "nobody",
		Password:    "pass",
		BindDNHint:  "CN=Nobody,DC=test,DC=local",
	})

	if result.ChallengeID != "fail-test" {
		t.Errorf("expected challenge_id=fail-test, got %s", result.ChallengeID)
	}
	if result.Success {
		t.Error("expected success=false for unreachable LDAP")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message")
	}
}
