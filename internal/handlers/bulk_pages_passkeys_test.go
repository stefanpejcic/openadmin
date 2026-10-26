package handlers

import "testing"

func TestUsersBulkRoutePasskeys(t *testing.T) {
	call, _ := usersBulkRoute("passkeys", "", "alice")
	if call == nil || call.Path != "/users/alice/account-setting/passkeys" {
		t.Fatalf("unexpected call: %+v", call)
	}
}

func TestUsersBulkRouteImunify(t *testing.T) {
	call, _ := usersBulkRoute("imunify", "", "alice")
	if call == nil || call.Method != "POST" || call.Path != "/security/imunify/alice" {
		t.Fatalf("unexpected call: %+v", call)
	}
}
