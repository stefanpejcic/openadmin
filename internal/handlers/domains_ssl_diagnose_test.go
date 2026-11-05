package handlers

import "testing"

func TestDiagnoseACMEPicksTheNewestErrorForTheDomain(t *testing.T) {
	logs := `{"level":"error","ts":1793430000,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"example.com","error":"HTTP 429 urn:ietf:params:acme:error:rateLimited - too many certificates"}
{"level":"info","ts":1793430100,"logger":"tls.obtain","msg":"acquiring lock","identifier":"example.com"}
{"level":"error","ts":1793430200,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"other.com","error":"NXDOMAIN"}
{"level":"error","ts":1793430300,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"www.example.com","error":"HTTP 403 urn:ietf:params:acme:error:unauthorized - Invalid response from http://www.example.com/.well-known/acme-challenge/x: 404"}
not json`
	d := diagnoseACME(logs, "example.com")
	if !d.Found || d.Time != "2026-10-31 07:05 UTC" {
		t.Fatalf("got %+v", d)
	}
	if d.Reason != acmeReasons[9].reason {
		t.Fatalf("reason: %q", d.Reason)
	}
	if got := diagnoseACME(logs, "missing.com"); got.Found {
		t.Fatalf("no error for that domain: %+v", got)
	}

	retry := `{"level":"error","ts":1793450797,"logger":"tls.obtain","msg":"will retry","error":"[example.net] Obtain: creating new order: HTTP 400 urn:ietf:params:acme:error:rejectedIdentifier - Cannot issue"}`
	if d := diagnoseACME(retry, "example.net"); !d.Found || d.Reason != acmeReasons[3].reason {
		t.Fatalf("retry line: %+v", d)
	}
}
