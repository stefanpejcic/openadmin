package handlers

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func stubUserPodman(t *testing.T, fn func(args []string) string) {
	orig := userPodmanRun
	userPodmanRun = func(context string, args ...string) (string, string, int, error) {
		return fn(args), "", 0, nil
	}
	t.Cleanup(func() { userPodmanRun = orig })
}

func TestUserOwnImagesRowSkipsSharedStore(t *testing.T) {
	stubUserPodman(t, func(args []string) string {
		return `[{"Id":"a","ReadOnly":true,"Size":5000000000,"Containers":1},
		{"Id":"b","Size":1200000000,"Containers":0},
		{"Id":"c","Size":300000000,"Containers":2},
		{"Id":"c","Size":300000000,"Containers":2}]`
	})
	row, ok := userOwnImagesRow("alice")
	if !ok {
		t.Fatal("expected a row")
	}
	if row["Total"] != 2 || row["Active"] != 1 || row["RawSize"] != int64(1500000000) || row["RawReclaimable"] != int64(1200000000) {
		t.Fatalf("unexpected row: %+v", row)
	}
	if row["Size"] != "1.5GB" || row["Reclaimable"] != "1.2GB (80%)" {
		t.Fatalf("unexpected sizes: %+v", row)
	}
}

func TestServeUserDiskInfoReplacesImagesRow(t *testing.T) {
	origRun := userDiskInfoRun
	userDiskInfoRun = func(context string) (string, string, int, error) {
		return `{"Type":"Images","Total":32,"RawSize":10519736650,"Size":"10.52GB"}` + "\n" + `{"Type":"Containers","Total":2}` + "\n", "", 0, nil
	}
	t.Cleanup(func() { userDiskInfoRun = origRun })
	stubUserPodman(t, func(args []string) string {
		return `[{"Id":"a","ReadOnly":true,"Size":10519736650,"Containers":1}]`
	})

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	srv, client := newUsersTestServer(t, &Users{MySQL: mysqlDB}, "admin")
	resp, err := client.Get(srv.URL + "/client/disk/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var decoded []map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if decoded[0]["Type"] != "Images" || decoded[0]["RawSize"] != float64(0) || decoded[0]["Total"] != float64(0) {
		t.Fatalf("images row not replaced: %+v", decoded[0])
	}
}

const dfVerboseSample = `Images space usage:

REPOSITORY  TAG  IMAGE ID  CREATED  SIZE  SHARED SIZE  UNIQUE SIZE  CONTAINERS
docker.io/library/alpine  latest  b92841763c5c  3 weeks  8.722MB  8.71MB  11.87kB  0

Containers space usage:

CONTAINER ID  IMAGE  COMMAND  LOCAL VOLUMES  SIZE  CREATED  STATUS  NAMES

Local Volumes space usage:

VOLUME NAME          LINKS       SIZE
rasa_html_data       2           110.1MB
rasa_mysql_data      1           170MB
rasa_mail_data       0           0B
`

func TestParseDfVolumeSizes(t *testing.T) {
	got := parseDfVolumeSizes(dfVerboseSample)
	if len(got) != 3 || got["rasa_html_data"] != [2]int64{2, 110100000} || got["rasa_mysql_data"] != [2]int64{1, 170000000} || got["rasa_mail_data"] != [2]int64{0, 0} {
		t.Fatalf("unexpected sizes: %+v", got)
	}
}

func TestServeUserVolumes(t *testing.T) {
	stubUserPodman(t, func(args []string) string {
		switch strings.Join(args, " ") {
		case "volume ls --format json":
			return `[{"Name":"rasa_mail_data","Driver":"local","Mountpoint":"/m","Labels":{"purpose":"storage"}},
			{"Name":"rasa_html_data","Driver":"local","Mountpoint":"/h","Labels":{"description":"html","purpose":"storage","com.docker.compose.project":"rasa"}}]`
		case "system df -v":
			return dfVerboseSample
		case "ps -aq":
			return "abc\n"
		case "container inspect abc":
			return `[{"Name":"apache","State":{"Status":"running"},"Mounts":[{"Type":"volume","Name":"rasa_html_data","Destination":"/var/www/html/","RW":true},{"Type":"bind","Destination":"/etc/x"}]}]`
		}
		return ""
	})

	mysqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	srv, client := newUsersTestServer(t, &Users{MySQL: mysqlDB}, "admin")
	resp, err := client.Get(srv.URL + "/client/volumes/alice")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var vols []userVolume
	if err := json.Unmarshal(body, &vols); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(vols) != 2 || vols[0].Name != "rasa_html_data" || vols[0].Size != 110100000 || vols[0].Project != "rasa" || vols[0].Description != "html" {
		t.Fatalf("unexpected volumes: %+v", vols)
	}
	if len(vols[0].UsedBy) != 1 || vols[0].UsedBy[0].Container != "apache" || len(vols[1].UsedBy) != 0 {
		t.Fatalf("unexpected mounts: %+v", vols)
	}
}
