package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"openadmin/internal/paneldb"
)

const (
	ResellerCopy = "copy"
	ResellerDrop = "drop"
)

// resellerExport is a reseller as another server needs it, allowed plans go by name since ids differ per server
type resellerExport struct {
	Name         string         `json:"name"`
	PasswordHash string         `json:"password_hash"`
	Active       bool           `json:"active"`
	Config       map[string]any `json:"config,omitempty"`
	PlanNames    []string       `json:"plan_names,omitempty"`
	TemplateName string         `json:"template_name,omitempty"`
	Template     string         `json:"template,omitempty"`
}

func resellerFile(name string) string { return filepath.Join(paneldb.ResellerConfigDir, name+".json") }

const resellerPlansDir = "/etc/openpanel/openadmin/reseller_plans"

func (m *Manager) exportReseller(name string) (*resellerExport, error) {
	if !accountRe.MatchString(name) {
		return nil, errors.New("invalid reseller")
	}
	u, err := m.DB.UserByUsername(name)
	if err != nil || u.Role != "reseller" {
		return nil, fmt.Errorf("%s is not a reseller here", name)
	}
	out := &resellerExport{Name: name, PasswordHash: u.PasswordHash, Active: u.IsActive}
	if raw, err := os.ReadFile(resellerFile(name)); err == nil {
		_ = json.Unmarshal(raw, &out.Config)
	}
	if ids, ok := out.Config["allowed_plans"].([]any); ok {
		for _, id := range ids {
			var planName string
			if m.MySQL.QueryRow(`SELECT name FROM plans WHERE id = ?`, id).Scan(&planName) == nil {
				out.PlanNames = append(out.PlanNames, planName)
			}
		}
	}
	if plan, ok := out.Config["reseller_plan"].(string); ok && accountRe.MatchString(plan) {
		if raw, err := os.ReadFile(filepath.Join(resellerPlansDir, plan+".json")); err == nil {
			out.TemplateName, out.Template = plan, string(raw)
		}
	}
	return out, nil
}

// importReseller creates the reseller here if it's missing, an existing one keeps its login and limits and only gets the new account counted
func (m *Manager) importReseller(r resellerExport) error {
	if !accountRe.MatchString(r.Name) {
		return errors.New("invalid reseller")
	}
	existing, err := m.DB.UserByUsername(r.Name)
	if err == nil && existing.Role != "reseller" {
		return fmt.Errorf("%s is already an admin here", r.Name)
	}
	config := map[string]any{}
	if err == nil {
		if raw, readErr := os.ReadFile(resellerFile(r.Name)); readErr == nil {
			_ = json.Unmarshal(raw, &config)
		}
	} else {
		if err := m.DB.CreateUser(r.Name, r.PasswordHash, "reseller"); err != nil {
			return err
		}
		if !r.Active {
			_ = m.DB.SetActive(r.Name, false)
		}
		for k, v := range r.Config {
			config[k] = v
		}
		ids := []int64{}
		for _, plan := range r.PlanNames {
			var id int64
			if m.MySQL.QueryRow(`SELECT id FROM plans WHERE name = ?`, plan).Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		config["allowed_plans"] = ids
		if r.TemplateName != "" && accountRe.MatchString(r.TemplateName) {
			path := filepath.Join(resellerPlansDir, r.TemplateName+".json")
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				_ = os.MkdirAll(resellerPlansDir, 0755)
				_ = os.WriteFile(path, []byte(r.Template), 0644)
			}
		}
	}
	var count int
	if err := m.MySQL.QueryRow(`SELECT COUNT(*) FROM users WHERE owner = ?`, r.Name).Scan(&count); err == nil {
		config["current_accounts"] = count
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resellerFile(r.Name)), 0755); err != nil {
		return err
	}
	return os.WriteFile(resellerFile(r.Name), raw, 0644)
}

// adminRole says if a name is taken among this server's admins, empty when it's free
func (m *Manager) adminRole(name string) string {
	u, err := m.DB.UserByUsername(name)
	if err != nil {
		return ""
	}
	return u.Role
}

// finishReseller is the last step of a move, by now the account is on the destination still pointing at its old owner
func (m *Manager) finishReseller(job Job) error {
	if job.Owner == "" {
		return nil
	}
	if job.Reseller == ResellerCopy {
		var exported resellerExport
		if err := m.retryCallOut(job.From, "reseller_export", job.Owner, &exported); err != nil {
			return err
		}
		return m.retryCall(job.To, "reseller_import", exported)
	}
	return m.retryCall(job.To, "account_owner", accountArgs{Account: job.Account})
}

func (m *Manager) retryCallOut(id, action string, data, out any) error {
	var err error
	for i := 0; i < 12; i++ {
		if err = m.call(id, action, data, out); err == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return err
}
