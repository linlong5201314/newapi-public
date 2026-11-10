package nodedeploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestBuildVariableDiff(t *testing.T) {
	desired := map[string]string{
		"SESSION_SECRET":                  "new-secret",
		"CRYPTO_SECRET":                   "crypto-new",
		"MISE_PYTHON_GITHUB_ATTESTATIONS": "false",
		"UNCHANGED":                       "same",
	}
	existing := map[string]string{
		"SESSION_SECRET": "old-secret",
		"UNCHANGED":      "same",
		"EXTRA_VAR":      "legacy",
	}
	actions, warnings := BuildVariableDiff(desired, existing, false)
	byVariable := map[string]Action{}
	for _, action := range actions {
		byVariable[action.Variable] = action
	}
	if len(actions) != 4 {
		t.Fatalf("expected 4 actions, got %d: %+v", len(actions), actions)
	}
	if action := byVariable["SESSION_SECRET"]; action.Op != "update" || action.From != "old-secret" || action.To != "new-secret" || !action.Sensitive {
		t.Fatalf("unexpected SESSION_SECRET action: %+v", action)
	}
	if action := byVariable["CRYPTO_SECRET"]; action.Op != "create" || !action.Sensitive {
		t.Fatalf("unexpected CRYPTO_SECRET action: %+v", action)
	}
	if action := byVariable["MISE_PYTHON_GITHUB_ATTESTATIONS"]; action.Op != "create" || action.Sensitive {
		t.Fatalf("unexpected MISE action: %+v", action)
	}
	if action := byVariable["EXTRA_VAR"]; action.Op != "delete" || !action.Skipped {
		t.Fatalf("extra variable must be skipped delete when prune=false: %+v", action)
	}
	if _, exists := byVariable["UNCHANGED"]; exists {
		t.Fatal("unchanged variables must not appear in the diff")
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "EXTRA_VAR") {
		t.Fatalf("expected prune warning, got %v", warnings)
	}

	pruned, prunedWarnings := BuildVariableDiff(desired, existing, true)
	for _, action := range pruned {
		if action.Variable == "EXTRA_VAR" {
			if action.Skipped {
				t.Fatal("prune=true must not skip delete actions")
			}
			if len(prunedWarnings) != 0 {
				t.Fatalf("expected no warnings with prune=true, got %v", prunedWarnings)
			}
			return
		}
	}
	t.Fatal("expected delete action for EXTRA_VAR")
}

func TestMaskedPlanHidesSensitiveValues(t *testing.T) {
	plan := &Plan{
		PlanId: "deploy_test",
		Actions: []Action{
			{Op: "update", Resource: "variable", Variable: "SESSION_SECRET", From: "old", To: "new", Sensitive: true},
			{Op: "update", Resource: "variable", Variable: "MISE_PYTHON_GITHUB_ATTESTATIONS", From: "a", To: "b"},
		},
		Variables: map[string]string{"SESSION_SECRET": "new"},
	}
	masked := plan.Masked()
	if masked.Actions[0].To != "******" || masked.Actions[0].From != "******" {
		t.Fatalf("sensitive action not masked: %+v", masked.Actions[0])
	}
	if masked.Actions[1].To != "b" {
		t.Fatalf("non-sensitive action must stay visible: %+v", masked.Actions[1])
	}
	if masked.Variables != nil {
		t.Fatal("masked plan must not expose internal variables")
	}
	if plan.Actions[0].To != "new" {
		t.Fatal("Masked must not mutate the internal plan")
	}
}

func TestParseEnvTemplate(t *testing.T) {
	text := `
# comment
SQL_DSN=postgres://u:p@host:6543/db
export SESSION_SECRET="quoted-secret"
MISE_PYTHON_GITHUB_ATTESTATIONS=false
INVALID LINE WITHOUT EQUALS
1BAD=x
`
	vars := ParseEnvTemplate(text)
	if len(vars) != 3 {
		t.Fatalf("expected 3 variables, got %+v", vars)
	}
	if vars[0].Key != "SQL_DSN" || vars[0].Value != "postgres://u:p@host:6543/db" {
		t.Fatalf("unexpected SQL_DSN: %+v", vars[0])
	}
	if vars[1].Key != "SESSION_SECRET" || vars[1].Value != "quoted-secret" {
		t.Fatalf("unexpected SESSION_SECRET: %+v", vars[1])
	}
}

func TestResolveVariablesOverridesAndWarnings(t *testing.T) {
	template := []EnvVar{{Key: "A", Value: "1"}, {Key: "B", Value: ""}}
	values, warnings := ResolveVariables(template, map[string]string{"B": "override", "C": "3", "bad-name": "x"})
	if values["A"] != "1" || values["B"] != "override" || values["C"] != "3" {
		t.Fatalf("unexpected resolved values: %+v", values)
	}
	if _, exists := values["bad-name"]; exists {
		t.Fatal("invalid override names must be ignored")
	}
	foundWarning := false
	for _, warning := range warnings {
		if strings.Contains(warning, "bad-name") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected invalid-name warning, got %v", warnings)
	}
}

func TestDenylistAndAllowlist(t *testing.T) {
	if !IsDenylisted("proj_protected", []string{"proj_protected"}) {
		t.Fatal("exact denylist match must hit")
	}
	if !IsDenylisted("gate.ailong.love", []string{"ailong.love"}) {
		t.Fatal("domain suffix denylist match must hit")
	}
	if IsDenylisted("proj_other", []string{"proj_protected"}) {
		t.Fatal("unrelated target must not hit denylist")
	}
	node := &model.Node{ProjectId: "proj_1", ServiceId: "svc_1", Domain: "node.example.com"}
	if err := ValidateTargets(node, Options{Denylist: []string{"proj_1"}}); !errors.Is(err, ErrDenylisted) {
		t.Fatalf("expected ErrDenylisted, got %v", err)
	}
	if err := ValidateTargets(node, Options{Allowlist: []string{"proj_1"}}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("expected ErrNotAllowed for svc/domain outside allowlist, got %v", err)
	}
	allowed := Options{Allowlist: []string{"proj_1", "svc_1", "node.example.com"}}
	if err := ValidateTargets(node, allowed); err != nil {
		t.Fatalf("all targets allowed, got %v", err)
	}
}

func TestStoreExpiryAndKind(t *testing.T) {
	store := NewStore()
	plan := &Plan{PlanId: "deploy_x", ExpiresAt: 1}
	if err := store.PutDeploy(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetDeploy("deploy_x"); !errors.Is(err, ErrPlanExpired) {
		t.Fatalf("expected expired plan, got %v", err)
	}
	if _, err := store.GetFailover("deploy_x"); !errors.Is(err, ErrPlanExpired) {
		t.Fatalf("kind mismatch must fail, got %v", err)
	}
	fresh := &Plan{PlanId: "deploy_y", ExpiresAt: 4102444800}
	if err := store.PutDeploy(fresh); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetDeploy("deploy_y")
	if err != nil || loaded.PlanId != "deploy_y" {
		t.Fatalf("expected fresh plan, got %v err=%v", loaded, err)
	}
}
