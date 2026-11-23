package handlers

import "testing"

func TestCountMainDomainsSkipsSubdomains(t *testing.T) {
	if n := countMainDomains([]string{"a.com", "blog.a.com", "b.net"}); n != 2 {
		t.Fatalf("want 2, got %d", n)
	}
}

func TestPlanUsageItem(t *testing.T) {
	p := planUsageItem{Used: 9, Limit: 10}
	if p.Percent() != 90 || p.RingColor() != "stroke-red-500" {
		t.Fatalf("got %d %s", p.Percent(), p.RingColor())
	}
	if (planUsageItem{Used: 12, Limit: 10}).Percent() != 100 {
		t.Fatal("percent should cap at 100")
	}
	if !(planUsageItem{Used: 3}).Unlimited() {
		t.Fatal("0 limit is unlimited")
	}
}

func TestPlanLimitTypes(t *testing.T) {
	plan := map[string]interface{}{"a": "5", "b": int64(7), "c": nil}
	if planLimit(plan, "a") != 5 || planLimit(plan, "b") != 7 || planLimit(plan, "c") != 0 {
		t.Fatal("bad limits")
	}
}
