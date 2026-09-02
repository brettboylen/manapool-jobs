package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type JobParse struct {
	Title         string   `json:"title"`
	Department    string   `json:"department"`
	Location      string   `json:"location"`
	Hiring        bool     `json:"hiring"`
	Compensation  string   `json:"compensation"`
	HowToApply    string   `json:"how_to_apply"`
	Summary       string   `json:"summary"`
	Highlights    []string `json:"highlights"`
}

const llmSystem = `Extract the Mana Pool job posting from the page text.
Return JSON only. Keep summary under 400 characters. highlights is at most 4 short bullets.
hiring is true only when applications are open (Now Hiring), false when closed.
If a field is missing use an empty string or empty array.`

func parseJobWithLLM(ctx context.Context, client *http.Client, llmURL, model, page string) (JobParse, error) {
	var zero JobParse
	llmURL = strings.TrimRight(strings.TrimSpace(llmURL), "/")
	if llmURL == "" {
		return zero, fmt.Errorf("LLM_URL is empty")
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	body := map[string]any{
		"model":      model,
		"json":       true,
		"max_tokens": 450,
		"system":     llmSystem,
		"prompt":     page,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, llmURL+"/v1/chat", bytes.NewReader(raw))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LLM-Caller", "manapool-jobs")
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return zero, err
	}
	if resp.StatusCode >= 400 {
		return zero, fmt.Errorf("llm %s: %s", resp.Status, bytes.TrimSpace(respBody))
	}
	var wrap struct {
		OK    bool            `json:"ok"`
		JSON  json.RawMessage `json:"json"`
		Text  string          `json:"text"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return zero, err
	}
	if wrap.Error != "" && !wrap.OK {
		return zero, fmt.Errorf("%s", wrap.Error)
	}
	blob := wrap.JSON
	if len(bytes.TrimSpace(blob)) == 0 {
		blob = []byte(wrap.Text)
	}
	var parsed JobParse
	if err := json.Unmarshal(blob, &parsed); err != nil {
		return zero, fmt.Errorf("parse llm json: %w", err)
	}
	return parsed, nil
}

func mergeParse(listing Listing, parsed JobParse) JobParse {
	if strings.TrimSpace(parsed.Title) == "" {
		parsed.Title = listing.Title
	}
	if strings.TrimSpace(parsed.Department) == "" {
		parsed.Department = listing.Department
	}
	if strings.TrimSpace(parsed.Location) == "" {
		parsed.Location = listing.Location
	}
	// The listing badge is the open/closed source of truth. The model only
	// fills the prose fields.
	parsed.Hiring = listing.Hiring
	return parsed
}
