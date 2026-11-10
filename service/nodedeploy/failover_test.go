package nodedeploy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupFailoverTestDB(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.AutoMigrate(&model.Node{}, &model.NodeAudit{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
}

func TestFailoverPlanAndApplyWithMockCloudflare(t *testing.T) {
	setupFailoverTestDB(t)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	previousToken := operation_setting.CloudflareApiToken
	previousZone := operation_setting.CloudflareZoneName
	previousZoneId := operation_setting.CloudflareZoneId
	previousAccount := operation_setting.CloudflareAccountId
	previousNamespace := operation_setting.CloudflareKvNamespaceId
	previousKey := operation_setting.CloudflareKvNodesKey
	previousPrimary := operation_setting.NodePrimaryDomain
	previousDenylist := operation_setting.NodeDenylistList()
	t.Cleanup(func() {
		operation_setting.CloudflareApiToken = previousToken
		operation_setting.CloudflareZoneName = previousZone
		operation_setting.CloudflareZoneId = previousZoneId
		operation_setting.CloudflareAccountId = previousAccount
		operation_setting.CloudflareKvNamespaceId = previousNamespace
		operation_setting.CloudflareKvNodesKey = previousKey
		operation_setting.NodePrimaryDomain = previousPrimary
		_ = operation_setting.SetNodeDenylistJSON(marshalList(previousDenylist))
	})
	operation_setting.CloudflareApiToken = "cf-token"
	operation_setting.CloudflareZoneName = "example.com"
	operation_setting.CloudflareZoneId = ""
	operation_setting.CloudflareAccountId = "acct1"
	operation_setting.CloudflareKvNamespaceId = "ns1"
	operation_setting.CloudflareKvNodesKey = "nodes"
	operation_setting.NodePrimaryDomain = "gate.example.com"
	_ = operation_setting.SetNodeDenylistJSON("[]")

	var mu sync.Mutex
	var cnameContent string
	var kvValue string
	cfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeCF := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			writeCF([]map[string]any{{"id": "zone1", "name": "example.com"}})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone1/dns_records":
			mu.Lock()
			content := cnameContent
			mu.Unlock()
			if content == "" {
				content = "old.example.com"
			}
			writeCF([]map[string]any{{
				"id": "rec1", "type": "CNAME", "name": "gate.example.com",
				"content": content, "proxied": true, "ttl": 1,
			}})
		case r.Method == http.MethodPut && r.URL.Path == "/zones/zone1/dns_records/rec1":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			cnameContent, _ = body["content"].(string)
			mu.Unlock()
			writeCF(body)
		case r.URL.Path == "/accounts/acct1/storage/kv/namespaces/ns1/values/nodes":
			if r.Method == http.MethodGet {
				mu.Lock()
				value := kvValue
				mu.Unlock()
				if value == "" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(value))
				return
			}
			if r.Method == http.MethodPut {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				kvValue = string(raw)
				mu.Unlock()
				writeCF(nil)
				return
			}
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected cloudflare request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer cfServer.Close()
	t.Setenv("CLOUDFLARE_API_ENDPOINT", cfServer.URL)

	source := &model.Node{Name: "source", Provider: "railway", Domain: "old.example.com", Enabled: true, Healthy: true}
	target := &model.Node{Name: "target", Provider: "railway", Domain: "new.up.railway.app", Enabled: true, Healthy: true}
	if err := model.CreateNode(source); err != nil {
		t.Fatal(err)
	}
	if err := model.CreateNode(target); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildFailoverPlan(context.Background(), source.Id, target.Id)
	if err != nil {
		t.Fatalf("build failover plan: %v", err)
	}
	if plan.PrimaryDomain != "gate.example.com" || plan.FromTarget != "old.example.com" || plan.ToTarget != "new.up.railway.app" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Actions) != 2 {
		t.Fatalf("expected dns + worker_kv actions, got %+v", plan.Actions)
	}
	if !strings.Contains(plan.KVTo, `"activeNodeId":`+itoa(target.Id)) {
		t.Fatalf("KV plan must point at target node: %s", plan.KVTo)
	}

	result, err := ApplyFailover(context.Background(), plan.PlanId, true, 1, "root")
	if err != nil {
		t.Fatalf("apply failover: %v", err)
	}
	if result.FromTarget != "old.example.com" || result.ToTarget != "new.up.railway.app" || !result.KVUpdated {
		t.Fatalf("unexpected result: %+v", result)
	}
	mu.Lock()
	finalCNAME, finalKV := cnameContent, kvValue
	mu.Unlock()
	if finalCNAME != "new.up.railway.app" {
		t.Fatalf("CNAME not switched: %q", finalCNAME)
	}
	var table map[string]any
	if err := json.Unmarshal([]byte(finalKV), &table); err != nil {
		t.Fatalf("KV payload is not JSON: %v (%q)", err, finalKV)
	}
	if table["activeUrl"] != "https://new.up.railway.app" {
		t.Fatalf("unexpected activeUrl: %v", table["activeUrl"])
	}
}

func TestFailoverRefusesUnhealthyTargetAndDenylist(t *testing.T) {
	setupFailoverTestDB(t)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	previousPrimary := operation_setting.NodePrimaryDomain
	previousToken := operation_setting.CloudflareApiToken
	previousZone := operation_setting.CloudflareZoneName
	t.Cleanup(func() {
		operation_setting.NodePrimaryDomain = previousPrimary
		operation_setting.CloudflareApiToken = previousToken
		operation_setting.CloudflareZoneName = previousZone
		_ = operation_setting.SetNodeDenylistJSON("[]")
	})
	operation_setting.NodePrimaryDomain = "gate.example.com"
	operation_setting.CloudflareApiToken = "cf-token"
	operation_setting.CloudflareZoneName = "example.com"

	source := &model.Node{Name: "source", Provider: "railway", Domain: "old.example.com", Enabled: true, Healthy: true}
	target := &model.Node{Name: "target", Provider: "railway", Domain: "new.up.railway.app", Enabled: true, Healthy: false}
	if err := model.CreateNode(source); err != nil {
		t.Fatal(err)
	}
	if err := model.CreateNode(target); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildFailoverPlan(context.Background(), source.Id, target.Id); err == nil {
		t.Fatal("expected unhealthy target to be rejected at plan time")
	}

	_ = operation_setting.SetNodeDenylistJSON(`["new.up.railway.app"]`)
	if _, err := BuildFailoverPlan(context.Background(), source.Id, target.Id); !errorsIs(err, ErrDenylisted) {
		t.Fatalf("expected denylist rejection, got %v", err)
	}
}

func marshalList(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func errorsIs(err, target error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), target.Error())
}
