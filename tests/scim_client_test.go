package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/locke-inc/directory-connector/internal/scim"
)

func TestSCIMClient(t *testing.T) {
	t.Run("create user", func(t *testing.T) {
		var receivedBody map[string]interface{}
		var receivedAuth string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &receivedBody)

			if r.Method != "POST" || r.URL.Path != "/scim/v2/Users" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}

			w.WriteHeader(201)
			w.Write([]byte(`{"id":"created-id"}`))
		}))
		defer server.Close()

		client := scim.NewClient(config.LockeConfig{
			APIURL:    server.URL,
			SCIMToken: "test-token-123",
		})

		user := &scim.SCIMUser{
			UserName:   "jsmith",
			ExternalID: "guid-abc",
			Name:       scim.SCIMName{GivenName: "John", FamilyName: "Smith"},
			Emails:     []scim.SCIMEmail{{Value: "j@acme.com", Primary: true}},
			Active:     true,
		}

		err := client.CreateUser(user)
		if err != nil {
			t.Fatalf("create user failed: %v", err)
		}

		if receivedAuth != "Bearer test-token-123" {
			t.Errorf("auth = %q, want Bearer test-token-123", receivedAuth)
		}

		if receivedBody["userName"] != "jsmith" {
			t.Errorf("userName = %v, want jsmith", receivedBody["userName"])
		}
	})

	t.Run("delete user", func(t *testing.T) {
		var method, path string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method = r.Method
			path = r.URL.Path
			w.WriteHeader(204)
		}))
		defer server.Close()

		client := scim.NewClient(config.LockeConfig{
			APIURL:    server.URL,
			SCIMToken: "tok",
		})

		err := client.DeleteUser("jsmith")
		if err != nil {
			t.Fatalf("delete failed: %v", err)
		}
		if method != "DELETE" {
			t.Errorf("method = %s, want DELETE", method)
		}
		if path != "/scim/v2/Users/jsmith" {
			t.Errorf("path = %s, want /scim/v2/Users/jsmith", path)
		}
	})

	t.Run("patch active false", func(t *testing.T) {
		var receivedBody map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &receivedBody)
			w.WriteHeader(200)
		}))
		defer server.Close()

		client := scim.NewClient(config.LockeConfig{
			APIURL:    server.URL,
			SCIMToken: "tok",
		})

		err := client.PatchUserActive("jsmith", false)
		if err != nil {
			t.Fatalf("patch failed: %v", err)
		}

		ops, ok := receivedBody["Operations"].([]interface{})
		if !ok || len(ops) == 0 {
			t.Fatal("expected Operations array")
		}
		op := ops[0].(map[string]interface{})
		if op["op"] != "replace" {
			t.Errorf("op = %v, want replace", op["op"])
		}
		if op["path"] != "active" {
			t.Errorf("path = %v, want active", op["path"])
		}
		if op["value"] != false {
			t.Errorf("value = %v, want false", op["value"])
		}
	})

	t.Run("4xx error is not retried", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(404)
			w.Write([]byte(`{"detail":"not found"}`))
		}))
		defer server.Close()

		client := scim.NewClient(config.LockeConfig{
			APIURL:    server.URL,
			SCIMToken: "tok",
		})

		err := client.DeleteUser("nobody")
		if err == nil {
			t.Fatal("expected error for 404")
		}
		if callCount != 1 {
			t.Errorf("expected 1 call, got %d (should not retry 4xx)", callCount)
		}
	})
}
