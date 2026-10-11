package translator

import (
	json "encoding/json/v2"
	"strings"
)

func NormalizeDeveloperRole(body []byte) []byte {
	if !strings.Contains(string(body), `"developer"`) {
		return body
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	msgs, ok := m["messages"].([]any)
	if !ok {
		return body
	}
	changed := false
	for _, msg := range msgs {
		mm, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := mm["role"].(string); role == "developer" {
			mm["role"] = "system"
			changed = true
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(m, json.Deterministic(true))
	if err != nil {
		return body
	}
	return out
}
