package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openadmin/internal/paneldb"
)

func withScratchResellerPlans(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	origPlans, origResellers := ResellerPlansDir, paneldb.ResellerConfigDir
	ResellerPlansDir = filepath.Join(dir, "reseller_plans")
	paneldb.ResellerConfigDir = filepath.Join(dir, "resellers")
	os.MkdirAll(paneldb.ResellerConfigDir, 0755)
	t.Cleanup(func() { ResellerPlansDir, paneldb.ResellerConfigDir = origPlans, origResellers })
}

func postResellerPlansAPI(t *testing.T, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/plans/resellers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ServeAPIResellerPlans(rec, req)
	var out map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&out)
	return rec.Code, out
}

func TestAPIResellerPlansCreateListDelete(t *testing.T) {
	withScratchResellerPlans(t)

	code, out := postResellerPlansAPI(t, `{"action":"create","name":"basic","description":"10 accounts","max_accounts":10,"max_disk_blocks":5120000,"allowed_plans":"1,2"}`)
	if code != http.StatusOK || out["success"] != true {
		t.Fatalf("create failed: %d %v", code, out)
	}
	p, ok := readResellerPlan("basic")
	if !ok || p.MaxAccounts != 10 || p.MaxDiskBlocks != 5120000 || len(p.AllowedPlans) != 2 || p.MaxDiskGB() != "5" {
		t.Fatalf("unexpected plan: %+v", p)
	}

	if code, _ := postResellerPlansAPI(t, `{"action":"create","name":"basic"}`); code != http.StatusBadRequest {
		t.Fatalf("expected duplicate create to fail, got %d", code)
	}

	rec := httptest.NewRecorder()
	ServeAPIResellerPlans(rec, httptest.NewRequest(http.MethodGet, "/api/plans/resellers", nil))
	var list []ResellerPlan
	json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 || list[0].Name != "basic" {
		t.Fatalf("unexpected list: %+v", list)
	}

	if code, out := postResellerPlansAPI(t, `{"action":"delete","name":"basic"}`); code != http.StatusOK {
		t.Fatalf("delete failed: %d %v", code, out)
	}
	if _, ok := readResellerPlan("basic"); ok {
		t.Fatal("plan still exists after delete")
	}
}

func TestAPIResellerPlansRejectsBadName(t *testing.T) {
	withScratchResellerPlans(t)
	for _, name := range []string{"../etc", "has space", strings.Repeat("a", 65)} {
		if code, _ := postResellerPlansAPI(t, `{"action":"create","name":"`+name+`"}`); code != http.StatusBadRequest {
			t.Fatalf("expected %q to be rejected, got %d", name, code)
		}
	}
}

func TestAPIPlanIDsAcceptsAllForms(t *testing.T) {
	for in, want := range map[string]int{`"1,2"`: 2, `[1,2,3]`: 3, `["4"]`: 1, `null`: 0} {
		if got := apiPlanIDs(json.RawMessage(in)); len(got) != want {
			t.Fatalf("%s: expected %d ids, got %v", in, want, got)
		}
	}
}

func TestDeleteResellerPlanRefusedWhileInUse(t *testing.T) {
	withScratchResellerPlans(t)
	writeResellerPlan(ResellerPlan{Name: "pro"})
	os.WriteFile(filepath.Join(paneldb.ResellerConfigDir, "res1.json"), []byte(`{"reseller_plan":"pro"}`), 0644)

	ok, msg := deleteResellerPlan("pro")
	if ok || !strings.Contains(msg, "res1") {
		t.Fatalf("expected refusal naming res1, got ok=%v msg=%q", ok, msg)
	}
}

func TestUpdateResellerPlanReappliesToResellers(t *testing.T) {
	withScratchResellerPlans(t)
	logFile := withFakeOpenCLI(t)
	writeResellerPlan(ResellerPlan{Name: "pro", MaxAccounts: 5})
	os.WriteFile(filepath.Join(paneldb.ResellerConfigDir, "res1.json"), []byte(`{"reseller_plan":"pro"}`), 0644)

	ok, msg := saveResellerPlan(ResellerPlan{Name: "pro", MaxAccounts: 20}, false)
	if !ok {
		t.Fatalf("update failed: %s", msg)
	}
	args, _ := os.ReadFile(logFile)
	if got := strings.TrimSpace(string(args)); got != "admin update res1 --plan=pro" {
		t.Fatalf("unexpected opencli args: %q", got)
	}
	if p, _ := readResellerPlan("pro"); p.MaxAccounts != 20 {
		t.Fatalf("plan not saved: %+v", p)
	}
}

func TestAPISettingsResellersCreateWithPlan(t *testing.T) {
	h, createUser := newAPISettingsResellersHandler(t)
	adminToken := createUser("admin1", "admin")
	withResellersEnabled(t, "yes")
	withScratchResellerPlans(t)
	logFile := withFakeOpenCLI(t)
	writeResellerPlan(ResellerPlan{Name: "basic"})

	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings/resellers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		h.ServeSettingsResellers(rec, req)
		return rec
	}

	if rec := send(`{"action":"create","username":"res1","password":"secretpw","plan":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown plan to fail, got %d", rec.Code)
	}
	if rec := send(`{"action":"create","username":"res1","password":"secretpw","plan":"basic"}`); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	args, _ := os.ReadFile(logFile)
	if got := strings.TrimSpace(string(args)); got != "admin new res1 secretpw --reseller --plan=basic" {
		t.Fatalf("unexpected opencli args: %q", got)
	}

	if rec := send(`{"action":"change_plan","username":"res1","plan":"basic"}`); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	args, _ = os.ReadFile(logFile)
	if got := strings.TrimSpace(string(args)); got != "admin update res1 --plan=basic" {
		t.Fatalf("unexpected opencli args: %q", got)
	}
}

func TestSortResellerPlansUnlimitedIsBiggest(t *testing.T) {
	plans := []ResellerPlan{{Name: "b", MaxAccounts: 0}, {Name: "a", MaxAccounts: 50}, {Name: "c", MaxAccounts: 10}}
	sortResellerPlans(plans, "max_accounts", "asc")
	if plans[0].Name != "c" || plans[1].Name != "a" || plans[2].Name != "b" {
		t.Fatalf("unexpected order: %+v", plans)
	}
	sortResellerPlans(plans, "name", "desc")
	if plans[0].Name != "c" || plans[2].Name != "a" {
		t.Fatalf("unexpected order: %+v", plans)
	}
}
