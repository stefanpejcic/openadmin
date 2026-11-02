package handlers

import "testing"

func TestLastCaddyCertError(t *testing.T) {
	logs := `{"level":"info","ts":1793621000,"logger":"tls.obtain","msg":"acquiring lock","identifier":"example.net"}
{"level":"error","ts":1793621304.6,"logger":"tls.obtain","msg":"will retry","error":"[example.net] Obtain: [example.net] creating new order: attempt 1: HTTP 400 rejectedIdentifier (ca=https://acme-staging-v02.api.letsencrypt.org/directory)"}
{"level":"error","ts":1793621305.8,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"example.net","error":"HTTP 400 urn:ietf:params:acme:error:rejectedIdentifier"}
{"level":"error","ts":1793621306,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"other.org","error":"timeout"}
not json at all example.net`

	got := lastCaddyCertError(logs, "example.net")
	if !got.Found || got.Reason != "HTTP 400 urn:ietf:params:acme:error:rejectedIdentifier" || got.Time == "" {
		t.Fatalf("example.net: got %+v", got)
	}

	retryOnly := `{"level":"error","ts":1793621304.6,"logger":"tls.obtain","msg":"will retry","error":"[example.net] Obtain: [example.net] creating new order: HTTP 400 (ca=https://acme-staging-v02.api.letsencrypt.org/directory)"}`
	if got := lastCaddyCertError(retryOnly, "example.net"); got.Reason != "creating new order: HTTP 400" {
		t.Fatalf("retry line: got %+v", got)
	}

	if got := lastCaddyCertError(logs, "missing.com"); got.Found {
		t.Fatalf("missing.com: got %+v", got)
	}
}
