package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnalyzeUsesModerationAndStructuredResponses(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v1/moderations":
			if request["model"] != "omni-moderation-latest" {
				t.Errorf("moderation model = %#v", request["model"])
			}
			writeTestJSON(w, map[string]any{"results": []any{map[string]any{
				"flagged":         false,
				"categories":      map[string]bool{"violence": false},
				"category_scores": map[string]float64{"violence": 0.01},
			}}})
		case "/v1/responses":
			if request["store"] != false {
				t.Errorf("store = %#v, want false", request["store"])
			}
			text, ok := request["text"].(map[string]any)
			if !ok || text["format"] == nil {
				t.Errorf("structured output format missing: %#v", request["text"])
			}
			result := Verification{
				ChallengePassed:      true,
				Confidence:           0.94,
				Summary:              "Visible evidence satisfies the challenge.",
				ObservedActions:      []string{"completed action"},
				RuleResults:          []RuleResult{{Rule: "Complete action", Passed: true, Evidence: "Visible in frames"}},
				RequiresManualReview: false,
				ManualReviewReason:   "",
			}
			encoded, _ := json.Marshal(result)
			writeTestJSON(w, map[string]any{
				"status": "completed",
				"output": []any{map[string]any{
					"type": "message",
					"content": []any{map[string]any{
						"type": "output_text",
						"text": string(encoded),
					}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(Config{
		APIKey:            "test-key",
		BaseURL:           server.URL,
		VerificationModel: "gpt-5.6-sol",
		ModerationModel:   "omni-moderation-latest",
		Timeout:           time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := client.Analyze(context.Background(), VerificationInput{
		UserID:               "user-id",
		ChallengeTitle:       "Land the jump",
		ChallengeDescription: "Land one jump.",
		ChallengeRules:       json.RawMessage(`["Land the jump"]`),
		Caption:              "proof",
		Frames:               [][]byte{{1, 2, 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Verification.ChallengePassed || analysis.Verification.Confidence != 0.94 {
		t.Fatalf("unexpected analysis: %#v", analysis)
	}
	if len(calls) != 2 || calls[0] != "/v1/moderations" || calls[1] != "/v1/responses" {
		t.Fatalf("calls = %#v", calls)
	}
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
