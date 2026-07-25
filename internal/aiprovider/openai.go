package aiprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	apiKey            string
	baseURL           string
	verificationModel string
	moderationModel   string
	httpClient        *http.Client
}

type Config struct {
	APIKey            string
	BaseURL           string
	VerificationModel string
	ModerationModel   string
	Timeout           time.Duration
}

type VerificationInput struct {
	UserID               string
	ChallengeTitle       string
	ChallengeDescription string
	ChallengeRules       json.RawMessage
	Caption              string
	Frames               [][]byte
}

type RuleResult struct {
	Rule     string `json:"rule"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

type Verification struct {
	ChallengePassed      bool         `json:"challenge_passed"`
	Confidence           float64      `json:"confidence"`
	Summary              string       `json:"summary"`
	ObservedActions      []string     `json:"observed_actions"`
	RuleResults          []RuleResult `json:"rule_results"`
	RequiresManualReview bool         `json:"requires_manual_review"`
	ManualReviewReason   string       `json:"manual_review_reason"`
}

type Moderation struct {
	Flagged        bool               `json:"flagged"`
	Categories     map[string]bool    `json:"categories"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

type Analysis struct {
	Verification    Verification `json:"verification"`
	Moderation      Moderation   `json:"moderation"`
	Provider        string       `json:"provider"`
	Model           string       `json:"model"`
	ModerationModel string       `json:"moderation_model"`
	ResponseID      string       `json:"response_id"`
	Usage           Usage        `json:"usage"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		return nil, errors.New("OpenAI base URL is required")
	}
	if config.Timeout <= 0 {
		config.Timeout = 90 * time.Second
	}
	return &Client{
		apiKey:            config.APIKey,
		baseURL:           strings.TrimRight(config.BaseURL, "/"),
		verificationModel: config.VerificationModel,
		moderationModel:   config.ModerationModel,
		httpClient:        &http.Client{Timeout: config.Timeout},
	}, nil
}

func (c *Client) Analyze(ctx context.Context, input VerificationInput) (Analysis, error) {
	if len(input.Frames) == 0 {
		return Analysis{}, errors.New("at least one video frame is required")
	}
	moderation, err := c.moderate(ctx, input.Caption, input.Frames)
	if err != nil {
		return Analysis{}, fmt.Errorf("OpenAI moderation: %w", err)
	}
	verification, responseID, usage, err := c.verify(ctx, input)
	if err != nil {
		return Analysis{}, fmt.Errorf("OpenAI verification: %w", err)
	}
	if moderation.Flagged {
		verification.RequiresManualReview = true
		if verification.ManualReviewReason == "" {
			verification.ManualReviewReason = "OpenAI moderation flagged sampled content"
		}
	}
	return Analysis{
		Verification:    verification,
		Moderation:      moderation,
		Provider:        "openai",
		Model:           c.verificationModel,
		ModerationModel: c.moderationModel,
		ResponseID:      responseID,
		Usage:           usage,
	}, nil
}

func (c *Client) moderate(ctx context.Context, caption string, frames [][]byte) (Moderation, error) {
	content := make([]any, 0, len(frames)+1)
	if strings.TrimSpace(caption) != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": caption,
		})
	}
	for _, frame := range frames {
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]string{
				"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(frame),
			},
		})
	}
	request := map[string]any{
		"model": c.moderationModel,
		"input": content,
	}
	var response struct {
		Results []Moderation `json:"results"`
	}
	if err := c.post(ctx, "/v1/moderations", request, &response); err != nil {
		return Moderation{}, err
	}
	if len(response.Results) != 1 {
		return Moderation{}, errors.New("moderation response did not contain one result")
	}
	return response.Results[0], nil
}

func (c *Client) verify(ctx context.Context, input VerificationInput) (Verification, string, Usage, error) {
	content := make([]any, 0, len(input.Frames)+1)
	instructions := fmt.Sprintf(
		"Challenge title: %s\nChallenge description: %s\nChallenge rules JSON: %s\nUser caption: %s\nEvaluate only what is visibly supported by the sampled frames.",
		input.ChallengeTitle,
		input.ChallengeDescription,
		string(input.ChallengeRules),
		input.Caption,
	)
	content = append(content, map[string]any{"type": "input_text", "text": instructions})
	for _, frame := range input.Frames {
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(frame),
			"detail":    "high",
		})
	}
	request := map[string]any{
		"model":             c.verificationModel,
		"store":             false,
		"safety_identifier": safetyIdentifier(input.UserID),
		"reasoning":         map[string]string{"effort": "low"},
		"max_output_tokens": 1800,
		"input": []any{
			map[string]any{
				"role":    "system",
				"content": `You verify short gaming challenge clips for HeatCheck. Apply every supplied challenge rule literally and conservatively. Frames are ordered samples, not a complete video. Do not infer identity, age, race, health, intent, or other sensitive traits. Never treat a caption as proof. Mark requires_manual_review when temporal continuity, audio, hidden UI, ambiguous edits, missing evidence, or sample gaps prevent a reliable decision. Evidence must be concise and tied to visible frames.`,
			},
			map[string]any{"role": "user", "content": content},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "heatcheck_verification",
				"strict": true,
				"schema": verificationSchema(),
			},
		},
	}
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Usage  Usage  `json:"usage"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := c.post(ctx, "/v1/responses", request, &response); err != nil {
		return Verification{}, "", Usage{}, err
	}
	if response.Status != "completed" {
		return Verification{}, "", Usage{}, fmt.Errorf("response status was %q", response.Status)
	}
	var output string
	for _, item := range response.Output {
		for _, contentItem := range item.Content {
			if contentItem.Type == "output_text" {
				output += contentItem.Text
			}
		}
	}
	if output == "" {
		return Verification{}, "", Usage{}, errors.New("response did not contain output text")
	}
	var verification Verification
	if err := json.Unmarshal([]byte(output), &verification); err != nil {
		return Verification{}, "", Usage{}, fmt.Errorf("decode structured verification: %w", err)
	}
	return verification, response.ID, response.Usage, nil
}

func (c *Client) post(ctx context.Context, endpoint string, body any, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+endpoint,
		bytes.NewReader(encoded),
	)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func safetyIdentifier(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])
}

func verificationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"challenge_passed",
			"confidence",
			"summary",
			"observed_actions",
			"rule_results",
			"requires_manual_review",
			"manual_review_reason",
		},
		"properties": map[string]any{
			"challenge_passed": map[string]any{"type": "boolean"},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
			"summary": map[string]any{"type": "string"},
			"observed_actions": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"rule_results": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"rule", "passed", "evidence"},
					"properties": map[string]any{
						"rule":     map[string]any{"type": "string"},
						"passed":   map[string]any{"type": "boolean"},
						"evidence": map[string]any{"type": "string"},
					},
				},
			},
			"requires_manual_review": map[string]any{"type": "boolean"},
			"manual_review_reason":   map[string]any{"type": "string"},
		},
	}
}
