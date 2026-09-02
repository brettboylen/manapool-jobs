package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type postingStore interface {
	known(ctx context.Context) (map[string]knownPosting, error)
	upsert(ctx context.Context, listing Listing, parsed JobParse, notified bool) error
	touch(ctx context.Context, listing Listing) error
}

type Config struct {
	JobsURL      string
	LLMURL       string
	LLMModel     string
	DiscordURL   string
	DiscordRoute string
	HTTP         *http.Client
	Store        postingStore
}

type tickResult struct {
	Seen      int
	New       int
	Reopened  int
	Alerted   int
	Bootstrap bool
}

func run(ctx context.Context, cfg Config) (tickResult, error) {
	var res tickResult
	html, err := fetchHTML(cfg.HTTP, cfg.JobsURL)
	if err != nil {
		return res, err
	}
	origin := originOf(cfg.JobsURL)
	listings, err := parseListings(html, origin)
	if err != nil {
		return res, err
	}
	res.Seen = len(listings)

	known, err := cfg.Store.known(ctx)
	if err != nil {
		return res, err
	}
	res.Bootstrap = len(known) == 0

	for _, listing := range listings {
		prev, exists := known[listing.Slug]
		newSlug := !exists
		reopened := exists && !prev.Hiring && listing.Hiring
		if newSlug {
			res.New++
		}
		if reopened {
			res.Reopened++
		}
		needDetail := newSlug || reopened
		parsed := JobParse{Title: listing.Title, Department: listing.Department, Location: listing.Location, Hiring: listing.Hiring}
		if needDetail {
			detail, err := fetchHTML(cfg.HTTP, listing.URL)
			if err != nil {
				log.Printf("detail %s: %v", listing.Slug, err)
			} else if text := pageText(detail); text != "" {
				if p, err := parseJobWithLLM(ctx, cfg.HTTP, cfg.LLMURL, cfg.LLMModel, text); err != nil {
					log.Printf("llm %s: %v", listing.Slug, err)
				} else {
					parsed = mergeParse(listing, p)
				}
			}
		}

		alert := !res.Bootstrap && listing.Hiring && (newSlug || reopened)
		if alert {
			if err := postDiscord(ctx, cfg.HTTP, cfg.DiscordURL, cfg.DiscordRoute, listing, parsed); err != nil {
				return res, err
			}
			res.Alerted++
		}
		if needDetail {
			if err := cfg.Store.upsert(ctx, listing, parsed, alert); err != nil {
				return res, err
			}
			continue
		}
		if err := cfg.Store.touch(ctx, listing); err != nil {
			return res, err
		}
	}
	return res, nil
}

func originOf(jobsURL string) string {
	u := strings.TrimRight(jobsURL, "/")
	if strings.HasSuffix(u, "/jobs") {
		return strings.TrimSuffix(u, "/jobs")
	}
	return "https://manapool.com"
}

func envStr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func defaultHTTP() *http.Client {
	return &http.Client{Timeout: 45 * time.Second}
}
