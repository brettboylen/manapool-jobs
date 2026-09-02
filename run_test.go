package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestShouldAlert(t *testing.T) {
	cases := []struct {
		bootstrap, hiring, newSlug, reopened, want bool
	}{
		{false, true, true, false, true},
		{false, true, false, true, true},
		{false, false, true, false, false},
		{true, true, true, false, false},
		{false, true, false, false, false},
	}
	for _, c := range cases {
		got := !c.bootstrap && c.hiring && (c.newSlug || c.reopened)
		if got != c.want {
			t.Fatalf("%+v got %v", c, got)
		}
	}
}

func TestMergeParseKeepsListingHiring(t *testing.T) {
	got := mergeParse(Listing{Title: "Ops", Hiring: true, Location: "Remote"}, JobParse{Hiring: false, Summary: "hi"})
	if !got.Hiring || got.Title != "Ops" || got.Summary != "hi" {
		t.Fatalf("%+v", got)
	}
}

func TestParseJobWithLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("X-LLM-Caller") != "manapool-jobs" {
			t.Errorf("caller %s", r.Header.Get("X-LLM-Caller"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["json"] != true {
			t.Errorf("json flag %+v", body["json"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"json": JobParse{
				Title:        "Fulfillment",
				Compensation: "$55k-$150k",
				HowToApply:   "email support@manapool.com",
				Summary:      "Receive and verify cards in Newbury Park.",
				Highlights:   []string{"On site", "Equity"},
			},
		})
	}))
	defer srv.Close()
	got, err := parseJobWithLLM(context.Background(), srv.Client(), srv.URL, "gpt-4o-mini", "Work in Fulfillment")
	if err != nil {
		t.Fatal(err)
	}
	if got.Compensation != "$55k-$150k" || got.Summary == "" {
		t.Fatalf("%+v", got)
	}
}

func TestPostDiscord(t *testing.T) {
	var caller, route, content string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		caller = r.Header.Get("X-Discord-Caller")
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		route, _ = body["route"].(string)
		content, _ = body["content"].(string)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	err := postDiscord(context.Background(), srv.Client(), srv.URL, "default",
		Listing{Title: "Marketplace Support", URL: "https://manapool.com/jobs/marketplace-support"},
		JobParse{Summary: "Help buyers and sellers.", Location: "US Remote"})
	if err != nil {
		t.Fatal(err)
	}
	if caller != "manapool-jobs" || route != "default" {
		t.Fatalf("caller=%q route=%q", caller, route)
	}
	if !strings.Contains(content, "Marketplace Support") || !strings.Contains(content, "@everyone") {
		t.Fatalf("content %q", content)
	}
}

type memStore struct {
	mu   sync.Mutex
	rows map[string]knownPosting
}

func (m *memStore) known(context.Context) (map[string]knownPosting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]knownPosting{}
	for k, v := range m.rows {
		out[k] = v
	}
	return out, nil
}

func (m *memStore) upsert(_ context.Context, listing Listing, _ JobParse, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]knownPosting{}
	}
	m.rows[listing.Slug] = knownPosting{Slug: listing.Slug, Hiring: listing.Hiring}
	return nil
}

func (m *memStore) touch(_ context.Context, listing Listing) error {
	return m.upsert(context.Background(), listing, JobParse{}, false)
}

func TestRunBootstrapsThenAlertsNewHire(t *testing.T) {
	listingHTML, err := os.ReadFile("testdata/jobs.html")
	if err != nil {
		t.Fatal(err)
	}
	var llmCalls, discordCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/jobs":
			_, _ = w.Write(listingHTML)
		case strings.HasPrefix(r.URL.Path, "/jobs/"):
			_, _ = w.Write([]byte(`<html><h1>Work in ` + r.URL.Path + `</h1><p>Do the work.</p></html>`))
		case r.URL.Path == "/v1/chat":
			llmCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"json": JobParse{Summary: "parsed", Compensation: "n/a", HowToApply: "email"},
			})
		case r.URL.Path == "/v1/messages":
			discordCalls++
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	st := &memStore{}
	cfg := Config{
		JobsURL:    upstream.URL + "/jobs",
		LLMURL:     upstream.URL,
		DiscordURL: upstream.URL,
		HTTP:       upstream.Client(),
		Store:      st,
	}

	res, err := run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Bootstrap || res.New != 3 || res.Alerted != 0 {
		t.Fatalf("bootstrap %+v", res)
	}
	if discordCalls != 0 {
		t.Fatalf("bootstrap must not page, got %d", discordCalls)
	}

	delete(st.rows, "fulfillment-specialist")

	res, err = run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bootstrap || res.New != 1 || res.Alerted != 1 {
		t.Fatalf("second tick %+v", res)
	}
	if discordCalls != 1 {
		t.Fatalf("expected one alert, got %d", discordCalls)
	}
}

func TestRunReopenAlerts(t *testing.T) {
	listingHTML, err := os.ReadFile("testdata/jobs.html")
	if err != nil {
		t.Fatal(err)
	}
	listingHTML = []byte(strings.Replace(string(listingHTML),
		"Marketplace Support <span>Applications Closed</span>",
		"Marketplace Support <span>Now Hiring</span>", 1))
	var discordCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/jobs":
			_, _ = w.Write(listingHTML)
		case strings.HasPrefix(r.URL.Path, "/jobs/"):
			_, _ = w.Write([]byte(`<html><p>open again</p></html>`))
		case r.URL.Path == "/v1/chat":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "json": JobParse{Summary: "reopened"}})
		case r.URL.Path == "/v1/messages":
			discordCalls++
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	st := &memStore{rows: map[string]knownPosting{
		"fulfillment-specialist": {Slug: "fulfillment-specialist", Hiring: true},
		"marketplace-support":    {Slug: "marketplace-support", Hiring: false},
		"software-engineer":      {Slug: "software-engineer", Hiring: false},
	}}
	res, err := run(context.Background(), Config{
		JobsURL:    upstream.URL + "/jobs",
		LLMURL:     upstream.URL,
		DiscordURL: upstream.URL,
		HTTP:       upstream.Client(),
		Store:      st,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reopened != 1 || res.Alerted != 1 || res.New != 0 {
		t.Fatalf("%+v discord=%d", res, discordCalls)
	}
}
