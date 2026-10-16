package handlers

import "strings"

// AllowsEmails is true only when every address is on an owned domain
func (s *resellerScope) AllowsEmails(addresses []string) bool {
	if s == nil {
		return true
	}
	if len(addresses) == 0 {
		return false
	}
	for _, a := range addresses {
		if !s.AllowsEmail(strings.TrimSpace(a)) {
			return false
		}
	}
	return true
}

// queueMessageVisible shows a queued message to a reseller only when it was sent from their domain or every recipient is theirs, so other customers' addresses never leak
func (s *resellerScope) queueMessageVisible(msg map[string]interface{}) bool {
	if s == nil {
		return true
	}
	if sender, _ := msg["sender"].(string); sender != "" && s.AllowsEmail(sender) {
		return true
	}
	rcpts, _ := msg["recipients"].([]interface{})
	if len(rcpts) == 0 {
		return false
	}
	for _, rc := range rcpts {
		m, _ := rc.(map[string]interface{})
		addr, _ := m["address"].(string)
		if !s.AllowsEmail(addr) {
			return false
		}
	}
	return true
}

func (s *resellerScope) filterQueue(queue []map[string]interface{}) []map[string]interface{} {
	return filterRows(s, queue, s.queueMessageVisible)
}

// queueTargets turns a queue action into the ids it may touch, a reseller's "all" means all of their visible messages and any id they can't see refuses the whole action
func (s *resellerScope) queueTargets(queue []map[string]interface{}, scope string, ids []string) (targets []string, ok bool) {
	visible := map[string]bool{}
	for _, msg := range s.filterQueue(queue) {
		if id, _ := msg["queue_id"].(string); id != "" {
			visible[id] = true
		}
	}
	if scope == "all" {
		for id := range visible {
			targets = append(targets, id)
		}
		return targets, true
	}
	for _, id := range ids {
		if !visible[id] {
			return nil, false
		}
	}
	return ids, true
}
