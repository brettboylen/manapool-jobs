package main

import (
	"os"
	"strings"
	"testing"
)

func TestParseListings(t *testing.T) {
	html, err := os.ReadFile("testdata/jobs.html")
	if err != nil {
		t.Fatal(err)
	}
	ls, err := parseListings(string(html), "https://manapool.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 3 {
		t.Fatalf("got %d listings: %+v", len(ls), ls)
	}
	if ls[0].Slug != "fulfillment-specialist" || !ls[0].Hiring || ls[0].Title != "Fulfillment and Verification Specialist" {
		t.Fatalf("first %+v", ls[0])
	}
	if ls[0].Department != "Operations" || ls[0].Location != "Newbury Park, CA (near LA)" {
		t.Fatalf("meta %+v", ls[0])
	}
	if ls[0].URL != "https://manapool.com/jobs/fulfillment-specialist" {
		t.Fatalf("url %q", ls[0].URL)
	}
	if ls[1].Hiring || ls[1].Slug != "marketplace-support" {
		t.Fatalf("closed %+v", ls[1])
	}
	if ls[2].Title != "Senior or Principal Software Engineer" {
		t.Fatalf("eng %+v", ls[2])
	}
}

func TestParseLiveJobsPage(t *testing.T) {
	html, err := os.ReadFile("testdata/jobs_live.html")
	if err != nil {
		t.Fatal(err)
	}
	ls, err := parseListings(string(html), "https://manapool.com")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Listing{}
	for _, l := range ls {
		by[l.Slug] = l
	}
	f, ok := by["fulfillment-specialist"]
	if !ok || !f.Hiring || !strings.Contains(f.Title, "Fulfillment") {
		t.Fatalf("fulfillment %+v", f)
	}
	if _, ok := by["software-engineer"]; !ok {
		t.Fatalf("missing software-engineer in %+v", by)
	}
}

func TestParseListingsEmpty(t *testing.T) {
	if _, err := parseListings(`<html><a href="/seller-info">x</a></html>`, "https://manapool.com"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPageTextStripsChrome(t *testing.T) {
	got := pageText(`<html><script>var x=1</script><style>h1{}</style><h1>Work here</h1><p>Do the job.</p><footer>Seller Info Grade Yard</footer>`)
	if got != "Work here Do the job." {
		t.Fatalf("got %q", got)
	}
}

func TestOriginOf(t *testing.T) {
	if got := originOf("https://manapool.com/jobs"); got != "https://manapool.com" {
		t.Fatalf("got %q", got)
	}
}
