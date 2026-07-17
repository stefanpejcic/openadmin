package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAPISettingsGeneralGetReturnsCurrentValues(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "example.com", "openpanel")

	g := &APISettingsGeneral{DevMode: true}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/general", nil)
	rec := httptest.NewRecorder()
	g.ServeSettingsGeneral(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["port"] != "2083" || body["admin_port"] != "2087" || body["force_domain"] != "example.com" || body["dev_mode"] != "on" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestAPISettingsGeneralPostInvalidJSONReturns400(t *testing.T) {
	g := &APISettingsGeneral{}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/general", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.ServeSettingsGeneral(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAPISettingsGeneralPostInvalidPortReturns400(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "example.com", "openpanel")
	g := &APISettingsGeneral{}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/general", strings.NewReader(`{"2087_port":"not-a-number"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.ServeSettingsGeneral(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAPISettingsGeneralPostAppliesOnlyChangedValues(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "example.com", "openpanel")
	_, openadminFlag := withScratchGeneralRestartFlags(t)
	calls := withScratchGeneralSetters(t)

	g := &APISettingsGeneral{}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/general",
		strings.NewReader(`{"force_domain":"new.example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.ServeSettingsGeneral(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(calls.domain) != 1 || calls.domain[0] != "new.example.com" {
		t.Fatalf("expected domain to be set once to new.example.com, got %v", calls.domain)
	}
	if len(calls.port) != 0 {
		t.Fatalf("expected port unchanged (same value submitted), got %v", calls.port)
	}
	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if changes, ok := body["changes"].([]interface{}); !ok || len(changes) == 0 {
		t.Fatalf("expected non-empty changes list, got %v", body["changes"])
	}
	if _, err := os.Stat(openadminFlag); err != nil {
		t.Fatalf("expected openadmin restart flag to be written: %v", err)
	}
}

func TestAPISettingsGeneralPostNoChangesReportsMessage(t *testing.T) {
	withScratchGeneralGetters(t, "2083", "2087", "example.com", "openpanel")
	withScratchGeneralRestartFlags(t)
	withScratchGeneralSetters(t)

	g := &APISettingsGeneral{}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/general", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.ServeSettingsGeneral(rec, req)

	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["message"] != "No changes made." {
		t.Fatalf("expected 'No changes made.', got %v", body["message"])
	}
}
