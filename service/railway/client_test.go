package railway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type graphQLCall struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func newMockServer(t *testing.T, handler func(call graphQLCall, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call graphQLCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Errorf("decode GraphQL request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		handler(call, w)
	}))
}

func writeData(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestVariableUpsertAlwaysSendsSkipDeploys(t *testing.T) {
	var captured map[string]any
	server := newMockServer(t, func(call graphQLCall, w http.ResponseWriter) {
		if strings.Contains(call.Query, "variableUpsert") {
			input, _ := call.Variables["input"].(map[string]any)
			captured = input
			writeData(t, w, map[string]any{"variableUpsert": true})
			return
		}
		t.Errorf("unexpected query: %s", call.Query)
	})
	defer server.Close()

	client := NewClientWithEndpoint("token", server.URL)
	err := client.VariableUpsert(context.Background(), VariableUpsertInput{
		ProjectId: "proj", EnvironmentId: "env", ServiceId: "svc",
		Name: "SESSION_SECRET", Value: "secret", SkipDeploys: false,
	})
	if err != nil {
		t.Fatalf("variableUpsert: %v", err)
	}
	if captured["skipDeploys"] != true {
		t.Fatalf("skipDeploys must be forced true, got %#v", captured["skipDeploys"])
	}
	if captured["name"] != "SESSION_SECRET" || captured["value"] != "secret" {
		t.Fatalf("unexpected input: %#v", captured)
	}
}

func TestServiceDomainCreateOmitsProjectId(t *testing.T) {
	var captured map[string]any
	server := newMockServer(t, func(call graphQLCall, w http.ResponseWriter) {
		if strings.Contains(call.Query, "serviceDomainCreate") {
			captured, _ = call.Variables["input"].(map[string]any)
			writeData(t, w, map[string]any{"serviceDomainCreate": map[string]any{"id": "dom_1", "domain": "node.up.railway.app"}})
			return
		}
		t.Errorf("unexpected query: %s", call.Query)
	})
	defer server.Close()

	client := NewClientWithEndpoint("token", server.URL)
	port := 3000
	domain, err := client.ServiceDomainCreate(context.Background(), "env_1", "svc_1", &port)
	if err != nil {
		t.Fatalf("serviceDomainCreate: %v", err)
	}
	if domain == nil || domain.Domain != "node.up.railway.app" {
		t.Fatalf("unexpected domain: %+v", domain)
	}
	if _, exists := captured["projectId"]; exists {
		t.Fatalf("projectId must not be sent to serviceDomainCreate: %#v", captured)
	}
	if len(captured) != 3 {
		t.Fatalf("expected exactly environmentId/serviceId/targetPort, got %#v", captured)
	}
	if captured["environmentId"] != "env_1" || captured["serviceId"] != "svc_1" {
		t.Fatalf("unexpected input: %#v", captured)
	}
}

func TestWaitForDeploymentStateMachine(t *testing.T) {
	statuses := []string{"QUEUED", "BUILDING", "SUCCESS"}
	var mu sync.Mutex
	index := 0
	server := newMockServer(t, func(call graphQLCall, w http.ResponseWriter) {
		if !strings.Contains(call.Query, "deployments(") {
			t.Errorf("unexpected query: %s", call.Query)
			return
		}
		mu.Lock()
		status := statuses[min(index, len(statuses)-1)]
		index++
		mu.Unlock()
		writeData(t, w, map[string]any{"deployments": map[string]any{
			"edges": []any{map[string]any{"node": map[string]any{"id": "dep_new", "status": status}}},
		}})
	})
	defer server.Close()
	t.Setenv("NODE_DEPLOY_POLL_INTERVAL_MS", "1")

	client := NewClientWithEndpoint("token", server.URL)
	deployment, err := client.WaitForDeployment(context.Background(), DeploymentListInput{ProjectId: "p", EnvironmentId: "e", ServiceId: "s"}, "", 2*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if deployment.Status != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %s", deployment.Status)
	}
	mu.Lock()
	calls := index
	mu.Unlock()
	if calls < 3 {
		t.Fatalf("expected at least 3 polls (QUEUED/BUILDING/SUCCESS), got %d", calls)
	}
}

func TestWaitForDeploymentFailureAndTimeout(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		server := newMockServer(t, func(call graphQLCall, w http.ResponseWriter) {
			writeData(t, w, map[string]any{"deployments": map[string]any{
				"edges": []any{map[string]any{"node": map[string]any{"id": "dep_new", "status": "FAILED"}}},
			}})
		})
		defer server.Close()
		t.Setenv("NODE_DEPLOY_POLL_INTERVAL_MS", "1")
		client := NewClientWithEndpoint("token", server.URL)
		if _, err := client.WaitForDeployment(context.Background(), DeploymentListInput{}, "", time.Second); err == nil {
			t.Fatal("expected failure for FAILED deployment")
		}
	})

	t.Run("timeout when baseline never changes", func(t *testing.T) {
		server := newMockServer(t, func(call graphQLCall, w http.ResponseWriter) {
			writeData(t, w, map[string]any{"deployments": map[string]any{
				"edges": []any{map[string]any{"node": map[string]any{"id": "dep_old", "status": "SUCCESS"}}},
			}})
		})
		defer server.Close()
		t.Setenv("NODE_DEPLOY_POLL_INTERVAL_MS", "1")
		client := NewClientWithEndpoint("token", server.URL)
		_, err := client.WaitForDeployment(context.Background(), DeploymentListInput{}, "dep_old", 20*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected timeout error, got %v", err)
		}
	})
}

func TestVariablesAndGraphQLError(t *testing.T) {
	server := newMockServer(t, func(call graphQLCall, w http.ResponseWriter) {
		switch {
		case strings.Contains(call.Query, "variables("):
			writeData(t, w, map[string]any{"variables": map[string]any{"A": "1", "B": 2, "C": nil}})
		default:
			if err := json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": "Not Authorized"}}}); err != nil {
				t.Errorf("encode error: %v", err)
			}
		}
	})
	defer server.Close()
	client := NewClientWithEndpoint("token", server.URL)
	vars, err := client.Variables(context.Background(), "p", "e", "s")
	if err != nil {
		t.Fatalf("variables: %v", err)
	}
	if vars["A"] != "1" || vars["B"] != "2" || vars["C"] != "" {
		t.Fatalf("unexpected variables: %#v", vars)
	}
	if _, err := client.Environments(context.Background(), "p"); err == nil {
		t.Fatal("expected GraphQL error to surface")
	} else {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || len(apiErr.Errors) != 1 {
			t.Fatalf("expected APIError with one GraphQL error, got %v", err)
		}
	}
}
