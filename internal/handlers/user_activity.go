// This file implements the "Online" column on /users: which hosting
// accounts currently have a live login session in openpanel (the
// customer-facing panel, a separate Go binary from this admin). Session
// state was formerly (before openpanel's Python->Go port) mirrored into a
// MySQL "active_sessions" table; that table is no longer written to and is
// treated as legacy/dead by the rest of the OpenPanel codebase, so
// consulting it here always reported everyone offline. Sessions now live
// only in Redis, as "session:<user id>:<token>" hashes with a "username"
// field, written by openpanel's logUserLogin on every login/autologin and
// expired by Redis's own TTL -- see finishLoginSession/handleAutologin in
// openpanel/internal/modules/account/login.go.
package handlers

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// activeSessionUsernamesRun scans Redis for every live openpanel session
// and returns the set of usernames that own at least one, mapped to the
// literal string "active" (the value is always this same constant, to
// match /json/user_activity_status's existing response shape).
//
// A package var (not a plain function) so tests can stub it out instead of
// touching a real redis socket -- see features.go's
// invalidateOpenpanelUserFeaturesCacheRun for the same pattern.
var activeSessionUsernamesRun = func() (map[string]string, error) {
	conn, err := net.DialTimeout("unix", FeaturesRedisSocketPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, err
	}

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	status := map[string]string{}
	cursor := "0"
	for {
		reply, err := respCommand(rw, "SCAN", cursor, "MATCH", "session:*", "COUNT", "500")
		if err != nil {
			return nil, err
		}
		pair, ok := reply.([]interface{})
		if !ok || len(pair) != 2 {
			return nil, fmt.Errorf("redis: unexpected SCAN reply %#v", reply)
		}
		next, _ := pair[0].(string)
		keys, _ := pair[1].([]interface{})

		for _, k := range keys {
			key, _ := k.(string)
			if key == "" {
				continue
			}
			usernameReply, err := respCommand(rw, "HGET", key, "username")
			if err != nil {
				return nil, err
			}
			if username, ok := usernameReply.(string); ok && username != "" {
				status[username] = "active"
			}
		}

		if next == "" || next == "0" {
			break
		}
		cursor = next
	}
	return status, nil
}

// respCommand sends args to Redis as a RESP array-of-bulk-strings request
// (the standard way to issue any command) and returns the parsed reply.
func respCommand(rw *bufio.ReadWriter, args ...string) (interface{}, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := rw.WriteString(b.String()); err != nil {
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		return nil, err
	}
	return respRead(rw.Reader)
}

// respRead parses one RESP2 reply: simple string (+), error (-), integer
// (:), bulk string ($, nil as "$-1"), or array (*, nil as "*-1", recursing
// for nested elements). That covers everything SCAN/HGET can reply with --
// this is not a general-purpose RESP3 client.
func respRead(r *bufio.Reader) (interface{}, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, fmt.Errorf("redis: empty reply line")
	}

	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, fmt.Errorf("redis: %s", line[1:])
	case ':':
		n, err := strconv.ParseInt(line[1:], 10, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2) // +2 for the trailing \r\n
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		arr := make([]interface{}, n)
		for i := 0; i < n; i++ {
			v, err := respRead(r)
			if err != nil {
				return nil, err
			}
			arr[i] = v
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("redis: unknown reply type %q", line[0])
	}
}
