package handlers

import "testing"

func TestParseUserNetworks(t *testing.T) {
	inspect := `[{"name":"podman","driver":"bridge","subnets":[{"subnet":"10.88.0.0/16","gateway":"10.88.0.1"}]},
	{"name":"stefan_db","driver":"bridge","network_interface":"podman2","subnets":[{"subnet":"10.89.0.0/24","gateway":"10.89.0.1"}],"dns_enabled":true,
	 "labels":{"purpose":"internal","description":"apps to database"},
	 "containers":{"60a5":{"name":"mariadb","interfaces":{"eth0":{"subnets":[{"ipnet":"10.89.0.15/24","gateway":"10.89.0.1"}],"mac_address":"aa:bb"}}}}}]`
	nets, err := parseUserNetworks(inspect, map[string]string{"mariadb": "running"}, map[string][]string{"mariadb": {"127.0.0.1:32770->3306/tcp"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 1 || nets[0].Name != "stefan_db" {
		t.Fatalf("empty default network should be skipped, got %+v", nets)
	}
	n := nets[0]
	if n.Purpose != "internal" || !n.DNS || n.Subnets[0] != "10.89.0.0/24" || n.Gateways[0] != "10.89.0.1" {
		t.Fatalf("bad network %+v", n)
	}
	c := n.Containers[0]
	if c.Name != "mariadb" || c.State != "running" || c.IPs[0] != "10.89.0.15" || c.MAC != "aa:bb" || c.Ports[0] != "127.0.0.1:32770->3306/tcp" {
		t.Fatalf("bad container %+v", c)
	}
}
