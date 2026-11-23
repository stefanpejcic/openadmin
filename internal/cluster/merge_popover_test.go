package cluster

import "testing"

func TestParseTablesSkipsTemplateTables(t *testing.T) {
	page := []byte(`<main><table><thead><tr><th>Site</th></tr></thead><tbody><tr><td>a</td></tr></tbody></table>` +
		`<template x-if="speed"><table><thead><tr><td></td><td>Desktop</td></tr></thead><tbody><tr><td>Performance</td><td>90</td></tr></tbody></table></template></main>`)
	tables := parseTables(page)
	if len(tables) != 2 {
		t.Fatalf("want 2 tables, got %d", len(tables))
	}
	if tables[0].hasTemplate {
		t.Fatal("list table should be merged")
	}
	if !tables[1].hasTemplate {
		t.Fatal("popover table inside a template should be skipped")
	}
}
