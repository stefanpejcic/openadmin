package handlers

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const webserversTestCompose = `services:
  caddy:
    image: caddy
    ports: ["${HTTP_PORT}"]
    volumes:
      - type: volume
        source: webserver_data
        target: /etc/caddy
  nginx:
    image: nginx
    ports: ["${HTTP_PORT}", "${HTTPS_PORT}"]
    volumes: ["webserver_data:/etc/nginx/sites-available/"]
  apache:
    image: httpd
    ports: ["${PROXY_HTTP_PORT:-${HTTP_PORT}}"]
    volumes: ["webserver_data:/usr/local/apache2/conf/sites-available/"]
  backup:
    image: backup
    volumes: ["webserver_data:/backup/webserver"]
  varnish:
    image: varnish
    ports: ["${HTTP_PORT}"]
`

func TestWebserverServicesInComposeData(t *testing.T) {
	got, err := webserverServicesInComposeData([]byte(webserversTestCompose))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"apache", "nginx", "caddy"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAvailableWebserversFallback(t *testing.T) {
	dir := t.TempDir()
	if got := availableWebservers(filepath.Join(dir, "missing.yml")); !reflect.DeepEqual(got, fallbackWebservers) {
		t.Errorf("missing file: got %v", got)
	}
	empty := filepath.Join(dir, "empty.yml")
	os.WriteFile(empty, []byte("services:\n  backup:\n    image: x\n"), 0644)
	if got := availableWebservers(empty); !reflect.DeepEqual(got, fallbackWebservers) {
		t.Errorf("no webservers: got %v", got)
	}
}
