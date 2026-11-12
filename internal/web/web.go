// Package web is Tavily web search, shaped to cost few tokens: short answer, 3 results, clipped snippets.
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	endpoint = "https://api.tavily.com/search"
	snippet  = 300
)

// Source is one result link, shown to the user under the web search line.
type Source struct{ Title, URL string }

func (s Source) Domain() string {
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" {
		return s.URL
	}
	return strings.TrimPrefix(u.Host, "www.")
}

// Endpoint is overridable for tests.
var Endpoint = endpoint

func Search(query string, maxResults int) (string, []Source) {
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		return "error: TAVILY_API_KEY is not set", nil
	}
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": max(1, min(maxResults, 5)), "include_answer": "basic"})
	req, _ := http.NewRequest(http.MethodPost, Endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "error: tavily: " + err.Error(), nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("error: tavily %d: %s", resp.StatusCode, string(raw[:min(len(raw), 200)])), nil
	}
	var data struct {
		Answer  string
		Results []struct{ Title, URL, Content string }
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "error: tavily returned invalid json", nil
	}
	var lines []string
	var sources []Source
	if data.Answer != "" {
		lines = append(lines, data.Answer)
	}
	for _, r := range data.Results {
		lines = append(lines, fmt.Sprintf("- %s <%s>\n  %s", r.Title, r.URL, r.Content[:min(len(r.Content), snippet)]))
		sources = append(sources, Source{r.Title, r.URL})
	}
	if len(lines) == 0 {
		return "no results", nil
	}
	return strings.Join(lines, "\n"), sources
}
