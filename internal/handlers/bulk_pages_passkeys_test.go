package handlers

import "testing"

func TestUsersBulkRoutePasskeys(t *testing.T) {
	call, _ := usersBulkRoute("passkeys", "", "alice")
	if call == nil || call.Path != "/users/alice/account-setting/passkeys" {
		t.Fatalf("unexpected call: %+v", call)
	}
}
