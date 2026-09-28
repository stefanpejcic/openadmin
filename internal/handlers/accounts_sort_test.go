package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSortAdministrators(t *testing.T) {
	rows := []administratorRow{
		{Username: "bob", LastActionTime: "2026-09-27 10:00:00", TOTPEnabled: true},
		{Username: "Alice", LastActionTime: ""},
		{Username: "carol", LastActionTime: "2026-09-28 09:00:00", TOTPEnabled: true, PasskeysEnabled: true},
	}
	names := func() string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Username)
		}
		return strings.Join(out, ",")
	}

	sortAdministrators(rows, "username", "asc")
	if names() != "Alice,bob,carol" {
		t.Errorf("username asc: %s", names())
	}
	sortAdministrators(rows, "activity", "desc")
	if names() != "carol,bob,Alice" {
		t.Errorf("activity desc: %s", names())
	}
	sortAdministrators(rows, "security", "asc")
	if names() != "Alice,bob,carol" {
		t.Errorf("security asc: %s", names())
	}
}

func TestReadSortIgnoresUnknownColumns(t *testing.T) {
	col, dir := readSort(httptest.NewRequest("GET", "/resellers?sort=password&direction=desc", nil), resellerSortKeys)
	if col != "" || dir != "" {
		t.Errorf("expected an unknown column to be ignored, got %q %q", col, dir)
	}
	col, dir = readSort(httptest.NewRequest("GET", "/resellers?sort=accounts&direction=DESC", nil), resellerSortKeys)
	if col != "accounts" || dir != "desc" {
		t.Errorf("got %q %q", col, dir)
	}
}
