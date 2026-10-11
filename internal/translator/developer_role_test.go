package translator

import (
	json "encoding/json/v2"
	"testing"
)

func TestNormalizeDeveloperRole(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		unchanged bool
	}{
		{
			name: "developer becomes system",
			in:   `{"model":"hermes","messages":[{"role":"developer","content":"sys"},{"role":"user","content":"hi"}]}`,
		},
		{
			name:      "system role untouched",
			in:        `{"messages":[{"role":"system","content":"a"},{"role":"user","content":"b"}]}`,
			unchanged: true,
		},
		{
			name:      "no messages key",
			in:        `{"model":"hermes"}`,
			unchanged: true,
		},
		{
			name:      "developer word in content only",
			in:        `{"messages":[{"role":"user","content":"the developer said hi"}]}`,
			unchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeDeveloperRole([]byte(tt.in))
			if tt.unchanged {
				if string(got) != tt.in {
					t.Fatalf("want input unchanged, got %s", got)
				}
				return
			}
			var m map[string]any
			if err := json.Unmarshal(got, &m); err != nil {
				t.Fatalf("output is not valid JSON: %v (%s)", err, got)
			}
			msgs, _ := m["messages"].([]any)
			if len(msgs) != 2 {
				t.Fatalf("want 2 messages, got %d", len(msgs))
			}
			first, _ := msgs[0].(map[string]any)
			if first["role"] != "system" {
				t.Errorf("messages[0].role = %v, want system", first["role"])
			}
			second, _ := msgs[1].(map[string]any)
			if second["role"] != "user" {
				t.Errorf("messages[1].role = %v, want user", second["role"])
			}
		})
	}
}
