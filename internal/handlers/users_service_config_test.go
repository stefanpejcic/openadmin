package handlers

import (
	"strings"
	"testing"
)

const svcCfgCompose = `# top comment
services:
  web:
    image: nginx:${NGINX_VERSION}
    #depends_on:
    #  - "php-${DEFAULT_PHP}"
    ports:
      - "${HTTP_PORT}"
    deploy:
      resources:
        limits:
          cpus: "${WEB_CPU:-0.5}"
          memory: "${WEB_RAM:-1G}"
    environment:
      RANGE: "${FTP_PORT_RANGE}"

  # the database
  mysql:
    image: mysql
    environment:
      MYSQL_ROOT_PASSWORD: $MYSQL_ROOT_PASSWORD

networks:
  www: {}
`

const svcCfgEnv = `DEFAULT_PHP=8.4
HTTP_PORT="127.0.0.1:32780:80"
NGINX_VERSION="1.27"
WEB_CPU='0.5'
MYSQL_ROOT_PASSWORD="x"
`

func TestReadServiceConfig(t *testing.T) {
	cfg, err := readServiceConfig(svcCfgCompose, svcCfgEnv, "web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Compose, "web:\n  image: nginx:${NGINX_VERSION}\n  #depends_on:") || strings.Contains(cfg.Compose, "mysql") {
		t.Fatalf("compose %q", cfg.Compose)
	}
	want := []envRow{
		{Key: "NGINX_VERSION", Value: "1.27", Repo: "nginx"},
		{Key: "HTTP_PORT", Value: "127.0.0.1:32780:80", ReadOnly: true},
		{Key: "WEB_CPU", Value: "0.5"},
		{Key: "WEB_RAM", Missing: true},
		{Key: "FTP_PORT_RANGE", ReadOnly: true, Missing: true},
	}
	if len(cfg.Env) != len(want) {
		t.Fatalf("env %+v", cfg.Env)
	}
	for i := range want {
		if cfg.Env[i] != want[i] {
			t.Errorf("row %d: got %+v want %+v", i, cfg.Env[i], want[i])
		}
	}
	if _, err := readServiceConfig(svcCfgCompose, svcCfgEnv, "nope"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestApplyServiceConfig(t *testing.T) {
	edit := serviceConfig{
		Compose: "web:\n  image: nginx:${NGINX_VERSION}\n  ports:\n    - \"${HTTP_PORT}\"\n  restart: always\n",
		Values:  map[string]string{"NGINX_VERSION": "1.28", "HTTP_PORT": "127.0.0.1:32780:80", "WEB_CPU": "0.5", "WEB_RAM": "2G"},
	}
	compose, env, err := applyServiceConfig(svcCfgCompose, svcCfgEnv, "web", edit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compose, "    restart: always\n\n  # the database\n  mysql:") || strings.Contains(compose, "WEB_CPU") {
		t.Fatalf("compose\n%s", compose)
	}
	// unchanged lines keep their quoting, a new one is added at the end
	want := "DEFAULT_PHP=8.4\nHTTP_PORT=\"127.0.0.1:32780:80\"\nNGINX_VERSION=\"1.28\"\nWEB_CPU='0.5'\nMYSQL_ROOT_PASSWORD=\"x\"\nWEB_RAM=\"2G\"\n"
	if env != want {
		t.Fatalf("env %q", env)
	}
}

func TestApplyServiceConfigRejects(t *testing.T) {
	ok := "web:\n  image: x\n  ports:\n    - \"${HTTP_PORT}\"\n"
	cases := map[string]serviceConfig{
		"bad yaml":      {Compose: "web:\n  image: [\n"},
		"renamed":       {Compose: "web2:\n  image: x\n"},
		"two services":  {Compose: "web:\n  image: x\nmysql:\n  image: y\n"},
		"not a mapping": {Compose: "web: nginx\n"},
		"port changed":  {Compose: ok, Values: map[string]string{"HTTP_PORT": "0.0.0.0:80:80"}},
		"compose ports": {Compose: "web:\n  image: x\n  ports:\n    - \"0.0.0.0:80:80\"\n"},
		"ports dropped": {Compose: "web:\n  image: x\n"},
		"port range":    {Compose: ok, Values: map[string]string{"FTP_PORT_RANGE": "1-2"}},
		"bad key":       {Compose: ok, Values: map[string]string{"1KEY": "x"}},
		"newline":       {Compose: ok, Values: map[string]string{"NGINX_VERSION": "1\nEVIL=1"}},
	}
	for name, edit := range cases {
		if _, _, err := applyServiceConfig(svcCfgCompose, svcCfgEnv, "web", edit); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPidsFromEnv(t *testing.T) {
	compose := "services:\n  apache:\n    image: httpd\n    deploy:\n      resources:\n        limits:\n          pids: 100\n  cron:\n    deploy:\n      resources:\n        limits:\n          pids: 200\n"
	got, changed := pidsFromEnv(compose, "apache", "APACHE_PIDS")
	if !changed || !strings.Contains(got, `          pids: "${APACHE_PIDS:-100}"`) || !strings.Contains(got, "          pids: 200\n") {
		t.Fatalf("got\n%s", got)
	}
	// already read from .env, nothing to do
	if _, changed := pidsFromEnv(got, "apache", "APACHE_PIDS"); changed {
		t.Fatal("expected no change the second time")
	}
}
