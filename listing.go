package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

type Listing struct {
	Slug       string
	URL        string
	Title      string
	Department string
	Location   string
	Hiring     bool
	Hash       string
}

var (
	reJobCard = regexp.MustCompile(`(?is)<a\s+href="(/jobs/([a-z0-9][a-z0-9-]*))"[^>]*>(.*?)</a>`)
	reH3      = regexp.MustCompile(`(?is)<h3[^>]*>(.*?)</h3>`)
	reSpan    = regexp.MustCompile(`(?is)<span[^>]*>(.*?)</span>`)
	reTag     = regexp.MustCompile(`(?is)<[^>]+>`)
	reSpace   = regexp.MustCompile(`\s+`)
)

func parseListings(pageHTML, origin string) ([]Listing, error) {
	origin = strings.TrimRight(origin, "/")
	var out []Listing
	seen := map[string]bool{}
	for _, m := range reJobCard.FindAllStringSubmatch(pageHTML, -1) {
		slug := strings.ToLower(strings.TrimSpace(m[2]))
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		card := m[3]
		title, hiring := parseTitle(card)
		dept, loc := parseMeta(card)
		if title == "" {
			continue
		}
		l := Listing{
			Slug:       slug,
			URL:        origin + "/jobs/" + slug,
			Title:      title,
			Department: dept,
			Location:   loc,
			Hiring:     hiring,
		}
		l.Hash = listingHash(l)
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no /jobs/ cards on listing page")
	}
	return out, nil
}

func parseTitle(card string) (title string, hiring bool) {
	h := card
	if m := reH3.FindStringSubmatch(card); m != nil {
		h = m[1]
	}
	low := strings.ToLower(h)
	switch {
	case strings.Contains(low, "now hiring"):
		hiring = true
	case strings.Contains(low, "applications closed"):
		hiring = false
	}
	title = cleanText(reSpan.ReplaceAllString(h, " "))
	return title, hiring
}

func parseMeta(card string) (dept, loc string) {
	var texts []string
	for _, m := range reSpan.FindAllStringSubmatch(card, -1) {
		t := cleanText(m[1])
		if t == "" || isStatusBadge(t) {
			continue
		}
		texts = append(texts, t)
	}
	if len(texts) >= 1 {
		dept = texts[0]
	}
	if len(texts) >= 2 {
		loc = texts[len(texts)-1]
	}
	return dept, loc
}

func isStatusBadge(s string) bool {
	switch strings.ToLower(s) {
	case "now hiring", "applications closed":
		return true
	}
	return false
}

func listingHash(l Listing) string {
	hiring := "closed"
	if l.Hiring {
		hiring = "hiring"
	}
	return strings.Join([]string{l.Slug, l.Title, l.Department, l.Location, hiring}, "|")
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = reTag.ReplaceAllString(s, " ")
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
