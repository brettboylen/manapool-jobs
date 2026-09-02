package main

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxPageBytes = 2 << 20

var (
	reScript      = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reStyle       = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reNoscript    = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript>`)
	reComment     = regexp.MustCompile(`(?s)<!--.*?-->`)
)

func fetchHTML(client *http.Client, url string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "manapool-jobs/1.0 (+homelab daily watcher)")
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	return string(body), nil
}

func pageText(html string) string {
	s := reScript.ReplaceAllString(html, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reNoscript.ReplaceAllString(s, " ")
	s = reComment.ReplaceAllString(s, " ")
	s = cleanText(s)
	if i := strings.Index(s, "Seller Info"); i > 8 {
		s = strings.TrimSpace(s[:i])
	}
	if utf8.RuneCountInString(s) > 6000 {
		r := []rune(s)
		s = string(r[:6000])
	}
	return s
}
