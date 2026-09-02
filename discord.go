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

func postDiscord(ctx context.Context, client *http.Client, discordURL, route string, listing Listing, parsed JobParse) error {
	discordURL = strings.TrimRight(strings.TrimSpace(discordURL), "/")
	if discordURL == "" {
		return fmt.Errorf("DISCORD_URL is empty")
	}
	if route == "" {
		route = "default"
	}
	body := map[string]any{
		"route":   route,
		"content": "@everyone New Mana Pool job: **" + listing.Title + "**",
		"allowed_mentions": map[string]any{
			"parse": []string{"everyone"},
		},
		"embeds": []map[string]any{discordEmbed(listing, parsed)},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Discord-Caller", "manapool-jobs")
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord %s: %s", resp.Status, bytes.TrimSpace(respBody))
	}
	return nil
}

func discordEmbed(listing Listing, parsed JobParse) map[string]any {
	desc := strings.TrimSpace(parsed.Summary)
	if desc == "" {
		desc = listing.Title
	}
	if len(parsed.Highlights) > 0 {
		var b strings.Builder
		b.WriteString(desc)
		b.WriteByte('\n')
		for _, h := range parsed.Highlights {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			b.WriteString("\n• ")
			b.WriteString(h)
		}
		desc = b.String()
	}
	if len(desc) > 1800 {
		desc = desc[:1800]
	}
	fields := []map[string]any{}
	add := func(name, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if len(v) > 1024 {
			v = v[:1024]
		}
		fields = append(fields, map[string]any{"name": name, "value": v, "inline": true})
	}
	add("Location", firstNonEmpty(parsed.Location, listing.Location))
	add("Team", firstNonEmpty(parsed.Department, listing.Department))
	add("Pay", parsed.Compensation)
	if parsed.HowToApply != "" {
		fields = append(fields, map[string]any{"name": "Apply", "value": clip(parsed.HowToApply, 1024), "inline": false})
	}
	return map[string]any{
		"title":       listing.Title,
		"url":         listing.URL,
		"description": desc,
		"color":       0x40A0FF,
		"fields":      fields,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
