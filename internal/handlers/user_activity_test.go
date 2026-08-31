package handlers

import (
	"bufio"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeRedisServer emulates just enough of Redis's SCAN/HGET protocol (RESP2
// requests decoded with the package's own respRead, since a command is
// wire-identical to any other RESP array-of-bulk-strings reply) to exercise
// activeSessionUsernamesRun's cursor-following loop end to end, without a
// real redis socket -- see features_test.go for the same "never touch a
// real redis socket" rule applied to invalidateOpenpanelUserFeaturesCacheRun.
func fakeRedisServer(t *testing.T, ln net.Listener) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		cmd, err := respRead(r)
		if err != nil {
			return
		}
		args, _ := cmd.([]interface{})
		if len(args) == 0 {
			return
		}
		name, _ := args[0].(string)

		var reply string
		switch strings.ToUpper(name) {
		case "SCAN":
			cursor, _ := args[1].(string)
			if cursor == "0" {
				reply = "*2\r\n$1\r\n3\r\n*1\r\n$16\r\nsession:1:tokenA\r\n"
			} else {
				reply = "*2\r\n$1\r\n0\r\n*1\r\n$16\r\nsession:2:tokenB\r\n"
			}
		case "HGET":
			key, _ := args[1].(string)
			if key == "session:1:tokenA" {
				reply = "$5\r\nalice\r\n"
			} else {
				reply = "$3\r\nbob\r\n"
			}
		default:
			reply = "$-1\r\n"
		}
		if _, err := w.WriteString(reply); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

func TestActiveSessionUsernamesRunFollowsScanCursor(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "redis.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go fakeRedisServer(t, ln)

	orig := FeaturesRedisSocketPath
	FeaturesRedisSocketPath = sockPath
	defer func() { FeaturesRedisSocketPath = orig }()

	got, err := activeSessionUsernamesRun()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"alice": "active", "bob": "active"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestActiveSessionUsernamesRunUnreachableRedisReturnsError(t *testing.T) {
	orig := FeaturesRedisSocketPath
	FeaturesRedisSocketPath = filepath.Join(t.TempDir(), "no-such.sock")
	defer func() { FeaturesRedisSocketPath = orig }()

	if _, err := activeSessionUsernamesRun(); err == nil {
		t.Fatal("expected an error when the redis socket doesn't exist")
	}
}
