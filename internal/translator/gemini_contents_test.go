package translator

import (
	"testing"
)

func TestNormalizeGeminiContents(t *testing.T) {
	tests := []struct {
		name  string
		in    []GeminiContent
		check func(t *testing.T, out []GeminiContent)
	}{
		{
			name: "appends Continue when ending on a model text turn",
			in: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "hi"}}},
				{Role: "model", Parts: []GeminiPart{{Text: "hello"}}},
			},
			check: func(t *testing.T, out []GeminiContent) {
				if len(out) != 3 {
					t.Fatalf("expected 3 turns, got %d", len(out))
				}
				last := out[2]
				if last.Role != "user" || len(last.Parts) != 1 || last.Parts[0].Text != "Continue." {
					t.Fatalf("expected trailing user turn with Continue., got %+v", last)
				}
			},
		},
		{
			name: "answers a functionCall with its id",
			in: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "hi"}}},
				{Role: "model", Parts: []GeminiPart{{FunctionCall: &GeminiFunctionCall{ID: "call_1", Name: "get_weather", Args: map[string]any{}}}}},
			},
			check: func(t *testing.T, out []GeminiContent) {
				last := out[len(out)-1]
				if last.Role != "user" || len(last.Parts) != 1 || last.Parts[0].FunctionResponse == nil {
					t.Fatalf("expected trailing user FunctionResponse turn, got %+v", last)
				}
				fr := last.Parts[0].FunctionResponse
				if fr.Name != "get_weather" || fr.ID != "call_1" {
					t.Fatalf("expected FunctionResponse name get_weather id call_1, got %+v", fr)
				}
				if fr.Response == nil || fr.Response.Result != "Continue." {
					t.Fatalf("expected Response Result Continue., got %+v", fr.Response)
				}
			},
		},
		{
			name: "answers every functionCall in the turn",
			in: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "hi"}}},
				{Role: "model", Parts: []GeminiPart{
					{FunctionCall: &GeminiFunctionCall{ID: "a", Name: "fn_a", Args: map[string]any{}}},
					{FunctionCall: &GeminiFunctionCall{ID: "b", Name: "fn_b", Args: map[string]any{}}},
				}},
			},
			check: func(t *testing.T, out []GeminiContent) {
				last := out[len(out)-1]
				if last.Role != "user" || len(last.Parts) != 2 {
					t.Fatalf("expected 2 FunctionResponse parts, got %+v", last)
				}
				if last.Parts[0].FunctionResponse.Name != "fn_a" || last.Parts[1].FunctionResponse.Name != "fn_b" {
					t.Fatalf("unexpected FunctionResponse order: %+v", last.Parts)
				}
			},
		},
		{
			name: "model turn carrying text and a call at once",
			in: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "hi"}}},
				{Role: "model", Parts: []GeminiPart{
					{Text: "let me think"},
					{FunctionCall: &GeminiFunctionCall{ID: "c", Name: "fn_c", Args: map[string]any{}}},
				}},
			},
			check: func(t *testing.T, out []GeminiContent) {
				last := out[len(out)-1]
				if last.Role != "user" || len(last.Parts) != 1 || last.Parts[0].FunctionResponse == nil {
					t.Fatalf("expected single FunctionResponse part, got %+v", last)
				}
				if last.Parts[0].FunctionResponse.Name != "fn_c" {
					t.Fatalf("unexpected FunctionResponse: %+v", last.Parts[0].FunctionResponse)
				}
			},
		},
		{
			name: "lone model turn gets both brackets",
			in: []GeminiContent{
				{Role: "model", Parts: []GeminiPart{{Text: "solo"}}},
			},
			check: func(t *testing.T, out []GeminiContent) {
				if len(out) != 3 {
					t.Fatalf("expected 3 turns, got %d", len(out))
				}
				if out[0].Role != "user" || len(out[0].Parts) != 1 || out[0].Parts[0].Text != "..." {
					t.Fatalf("expected leading user turn with ..., got %+v", out[0])
				}
				if out[1].Role != "model" {
					t.Fatalf("expected middle model turn, got %+v", out[1])
				}
				if out[2].Role != "user" || out[2].Parts[0].Text != "Continue." {
					t.Fatalf("expected trailing user turn with Continue., got %+v", out[2])
				}
			},
		},
		{
			name: "payload already ending on user is left alone",
			in: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "hi"}}},
				{Role: "model", Parts: []GeminiPart{{Text: "hello"}}},
				{Role: "user", Parts: []GeminiPart{{Text: "more"}}},
			},
			check: func(t *testing.T, out []GeminiContent) {
				if len(out) != 3 {
					t.Fatalf("expected 3 turns, got %d", len(out))
				}
				if out[2].Parts[0].Text != "more" {
					t.Fatalf("expected trailing user turn untouched, got %+v", out[2])
				}
			},
		},
		{
			name: "adjacent model turns merge before the guard runs",
			in: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "hi"}}},
				{Role: "model", Parts: []GeminiPart{{Text: "a"}}},
				{Role: "model", Parts: []GeminiPart{{Text: "b"}}},
			},
			check: func(t *testing.T, out []GeminiContent) {
				if len(out) != 3 {
					t.Fatalf("expected 3 turns, got %d", len(out))
				}
				if out[1].Role != "model" || len(out[1].Parts) != 2 || out[1].Parts[0].Text != "a" || out[1].Parts[1].Text != "b" {
					t.Fatalf("expected merged model turn with both parts, got %+v", out[1])
				}
				if out[2].Role != "user" || out[2].Parts[0].Text != "Continue." {
					t.Fatalf("expected trailing user turn with Continue., got %+v", out[2])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := NormalizeGeminiContents(tt.in)
			tt.check(t, out)
		})
	}
}
