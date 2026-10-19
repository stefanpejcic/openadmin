package handlers

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"openadmin/internal/auth"
	"openadmin/internal/podman"
)

// dumpPeekSize is held back so a dump that dies right away is reported before any download headers go out
const dumpPeekSize = 64 << 10

var mysqlDumpBinary = map[string]string{"mysql": "mysqldump", "mariadb": "mariadb-dump"}

// dbTarget is a user's database that export/import act on
type dbTarget struct {
	Owner, Context, Service, Database string
}

// resolveDBTarget checks the owner exists and is the caller's to manage, the database is on their disk and their mysqld is up
func (d *Databases) resolveDBTarget(r *http.Request, owner, database string) (dbTarget, error) {
	t := dbTarget{Owner: owner, Database: database}
	if d.MySQL == nil || owner == "" || database == "" {
		return t, errors.New("Owner and database are required.")
	}
	if !scopeFor(r, d.MySQL).AllowsUser(owner) {
		return t, errors.New("User " + owner + " was not found.")
	}
	var ctx string
	if err := d.MySQL.QueryRow(`SELECT server FROM users WHERE username = ? LIMIT 1`, owner).Scan(&ctx); err != nil || ctx == "" || strings.ContainsAny(ctx, "/.") {
		return t, errors.New("User " + owner + " was not found.")
	}
	t.Context = ctx
	found := false
	for _, row := range readUserDatabases(ctx) {
		if row.Name == database {
			found = true
			break
		}
	}
	if !found {
		return t, errors.New("Database " + database + " was not found for user " + owner + ".")
	}
	t.Service = envOr(userEnv(ctx), "MYSQL_TYPE", "mysql")
	if mysqlDumpBinary[t.Service] == "" {
		return t, errors.New("Unsupported database service: " + t.Service)
	}
	if _, err := os.Stat(filepath.Join(UserHomeDir, ctx, "sockets", "mysqld", "mysqld.sock")); err != nil {
		return t, errors.New("The " + t.Service + " service of user " + owner + " is not running.")
	}
	return t, nil
}

// podmanExec is podman exec in the user's container, killed when ctx ends
var podmanExec = func(ctx context.Context, userContext string, args ...string) (*exec.Cmd, error) {
	c, err := podman.Command(userContext, append([]string{"exec"}, args...)...)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, c.Path, c.Args[1:]...)
	cmd.Env = c.Env
	return cmd, nil
}

// noDeadlines lifts the server's 30s timeouts, a big dump takes longer than that
func noDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
}

// ServeExport handles POST /databases/export: streams a dump straight to the browser, gzipped on the fly if asked
func (d *Databases) ServeExport(w http.ResponseWriter, r *http.Request) {
	back := func(msg string) {
		auth.AddFlash(w, r, d.Sessions, msg, "error")
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	}
	t, err := d.resolveDBTarget(r, r.FormValue("owner"), r.FormValue("database"))
	if err != nil {
		back(err.Error())
		return
	}
	gz := r.FormValue("format") == "gzip"
	noDeadlines(w)

	cmd, err := podmanExec(r.Context(), t.Context, t.Service, mysqlDumpBinary[t.Service], "--single-transaction", "--routines", "--triggers", "--", t.Database)
	if err != nil {
		back("Export of " + t.Database + " failed: " + err.Error())
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		back("Export of " + t.Database + " failed: " + err.Error())
		return
	}

	br := bufio.NewReaderSize(out, dumpPeekSize)
	var src io.Reader = br
	var waitErr error
	waited := false
	if head, peekErr := br.Peek(dumpPeekSize); peekErr != nil {
		// whole dump fit in head so check the exit code first, Wait closes the pipe so send head itself
		waitErr, waited, src = cmd.Wait(), true, bytes.NewReader(head)
		if waitErr != nil || len(head) == 0 {
			back("Export of " + t.Database + " failed: " + strings.TrimSpace(stderr.String()))
			return
		}
	}

	filename := t.Owner + "_" + t.Database + ".sql"
	w.Header().Set("Content-Type", "application/sql")
	if gz {
		filename += ".gz"
		w.Header().Set("Content-Type", "application/gzip")
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)

	var dst io.Writer = w
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(w)
		dst = zw
	}
	_, copyErr := io.Copy(dst, src)
	if zw != nil && copyErr == nil {
		copyErr = zw.Close()
	}
	if copyErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if !waited {
		waitErr = cmd.Wait()
	}
	if copyErr != nil || waitErr != nil {
		// headers are out already, dropping the connection makes the browser mark the download failed instead of keeping a cut off file
		panic(http.ErrAbortHandler)
	}
}

// ServeImport handles POST /databases/import?owner=&database=: streams the uploaded .sql or .sql.gz into the database without saving it first
func (d *Databases) ServeImport(w http.ResponseWriter, r *http.Request) {
	t, err := d.resolveDBTarget(r, r.URL.Query().Get("owner"), r.URL.Query().Get("database"))
	if err != nil {
		writeJSONError2(w, http.StatusBadRequest, err.Error())
		return
	}
	noDeadlines(w)

	mr, err := r.MultipartReader()
	if err != nil {
		writeJSONError2(w, http.StatusBadRequest, "Expected a file upload.")
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			writeJSONError2(w, http.StatusBadRequest, "No database file uploaded.")
			return
		}
		if part.FormName() != "db_file" {
			continue
		}
		name := strings.ToLower(part.FileName())
		if !strings.HasSuffix(name, ".sql") && !strings.HasSuffix(name, ".sql.gz") {
			writeJSONError2(w, http.StatusBadRequest, "Only .sql and .sql.gz files can be imported.")
			return
		}
		var src io.Reader = part
		if strings.HasSuffix(name, ".gz") {
			zr, err := gzip.NewReader(part)
			if err != nil {
				writeJSONError2(w, http.StatusBadRequest, "Not a valid .gz file: "+err.Error())
				return
			}
			defer zr.Close()
			src = zr
		}

		// the mysql and mariadb images name their client after themselves
		cmd, err := podmanExec(r.Context(), t.Context, "-i", t.Service, t.Service, "--", t.Database)
		if err != nil {
			writeJSONError2(w, http.StatusInternalServerError, err.Error())
			return
		}
		var stderr bytes.Buffer
		cmd.Stdin, cmd.Stderr = src, &stderr
		if err := cmd.Run(); err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = err.Error()
			}
			writeJSONError2(w, http.StatusBadRequest, "Import into "+t.Database+" failed: "+detail)
			return
		}
		msg := "Imported " + part.FileName() + " into " + t.Database + " of user " + t.Owner + "."
		auth.AddFlash(w, r, d.Sessions, msg, "success")
		writeJSON(w, map[string]interface{}{"success": true, "message": msg})
		return
	}
}
