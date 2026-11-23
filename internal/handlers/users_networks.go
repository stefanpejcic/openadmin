package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"openadmin/internal/auth"
	"openadmin/internal/paneldb"
)

type networkContainer struct {
	Name  string   `json:"name"`
	State string   `json:"state"`
	IPs   []string `json:"ips"`
	MAC   string   `json:"mac"`
	Ports []string `json:"ports"`
}

type userNetwork struct {
	Name        string             `json:"name"`
	Driver      string             `json:"driver"`
	Interface   string             `json:"interface"`
	Subnets     []string           `json:"subnets"`
	Gateways    []string           `json:"gateways"`
	Internal    bool               `json:"internal"`
	DNS         bool               `json:"dns"`
	IPv6        bool               `json:"ipv6"`
	Created     string             `json:"created_at"`
	Description string             `json:"description"`
	Purpose     string             `json:"purpose"`
	Labels      map[string]string  `json:"labels"`
	Containers  []networkContainer `json:"containers"`
}

type podmanNetworkInspect struct {
	Name             string `json:"name"`
	Driver           string `json:"driver"`
	NetworkInterface string `json:"network_interface"`
	Created          string `json:"created"`
	Subnets          []struct {
		Subnet  string `json:"subnet"`
		Gateway string `json:"gateway"`
	} `json:"subnets"`
	IPv6       bool              `json:"ipv6_enabled"`
	Internal   bool              `json:"internal"`
	DNS        bool              `json:"dns_enabled"`
	Labels     map[string]string `json:"labels"`
	Containers map[string]struct {
		Name       string `json:"name"`
		Interfaces map[string]struct {
			Subnets []struct {
				IPNet string `json:"ipnet"`
			} `json:"subnets"`
			MAC string `json:"mac_address"`
		} `json:"interfaces"`
	} `json:"containers"`
}

type podmanPsContainer struct {
	Names []string `json:"Names"`
	State string   `json:"State"`
	Ports []struct {
		HostIP        string `json:"host_ip"`
		ContainerPort int    `json:"container_port"`
		HostPort      int    `json:"host_port"`
		Range         int    `json:"range"`
		Protocol      string `json:"protocol"`
	} `json:"Ports"`
}

// containerPorts maps container name to state and its published ports, like podman ps prints them
func containerPorts(context string) (map[string]string, map[string][]string) {
	states, ports := map[string]string{}, map[string][]string{}
	out, _, code, err := userPodmanRun(context, "ps", "-a", "--format", "json")
	if err != nil || code != 0 {
		return states, ports
	}
	var list []podmanPsContainer
	if json.Unmarshal([]byte(out), &list) != nil {
		return states, ports
	}
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		name := c.Names[0]
		states[name] = c.State
		for _, p := range c.Ports {
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			cport := strconv.Itoa(p.ContainerPort) + "/" + proto
			if p.HostPort == 0 {
				ports[name] = append(ports[name], cport)
				continue
			}
			host := p.HostIP
			if host == "" {
				host = "0.0.0.0"
			}
			ports[name] = append(ports[name], fmt.Sprintf("%s:%d->%s", host, p.HostPort, cport))
		}
	}
	return states, ports
}

func parseUserNetworks(inspectOut string, states map[string]string, ports map[string][]string) ([]userNetwork, error) {
	var raw []podmanNetworkInspect
	if err := json.Unmarshal([]byte(inspectOut), &raw); err != nil {
		return nil, err
	}
	nets := make([]userNetwork, 0, len(raw))
	for _, n := range raw {
		// the default podman network is only worth showing when something uses it
		if n.Name == "podman" && len(n.Containers) == 0 {
			continue
		}
		labels := n.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		un := userNetwork{
			Name: n.Name, Driver: n.Driver, Interface: n.NetworkInterface, Created: n.Created,
			Internal: n.Internal, DNS: n.DNS, IPv6: n.IPv6, Labels: labels,
			Description: labels["description"], Purpose: labels["purpose"],
			Subnets: []string{}, Gateways: []string{}, Containers: []networkContainer{},
		}
		for _, s := range n.Subnets {
			un.Subnets = append(un.Subnets, s.Subnet)
			if s.Gateway != "" {
				un.Gateways = append(un.Gateways, s.Gateway)
			}
		}
		for _, c := range n.Containers {
			nc := networkContainer{Name: c.Name, State: states[c.Name], IPs: []string{}, Ports: ports[c.Name]}
			if nc.Ports == nil {
				nc.Ports = []string{}
			}
			for _, iface := range c.Interfaces {
				for _, s := range iface.Subnets {
					ip, _, _ := strings.Cut(s.IPNet, "/")
					nc.IPs = append(nc.IPs, ip)
				}
				if nc.MAC == "" {
					nc.MAC = iface.MAC
				}
			}
			un.Containers = append(un.Containers, nc)
		}
		sort.Slice(un.Containers, func(i, j int) bool { return un.Containers[i].Name < un.Containers[j].Name })
		nets = append(nets, un)
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Name < nets[j].Name })
	return nets, nil
}

// ServeUserNetworks handles GET /client/networks/{username}
func (u *Users) ServeUserNetworks(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	currentUser := auth.CurrentUser(r)
	if !paneldb.CheckIfOwnerForUser(u.MySQL, username, currentUser.Username, currentUser.Role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	context, _ := queryContextByUsername(u.MySQL, username)
	names, stderr, code, err := userPodmanRun(context, "network", "ls", "--format", "{{.Name}}")
	if err == nil && code != 0 {
		err = fmt.Errorf("podman network ls exited with status %d: %s", code, strings.TrimSpace(stderr))
	}
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	list := strings.Fields(names)
	if len(list) == 0 {
		writeJSON(w, []userNetwork{})
		return
	}
	out, stderr, code, err := userPodmanRun(context, append([]string{"network", "inspect"}, list...)...)
	if err == nil && code != 0 {
		err = fmt.Errorf("podman network inspect exited with status %d: %s", code, strings.TrimSpace(stderr))
	}
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	states, ports := containerPorts(context)
	nets, err := parseUserNetworks(out, states, ports)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "invalid JSON in podman output"})
		return
	}
	writeJSON(w, nets)
}
