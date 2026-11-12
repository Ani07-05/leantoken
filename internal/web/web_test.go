package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchFormatsAnswerAndSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"answer":"42","results":[{"title":"Docs","url":"https://www.example.com/a","content":"` + strings.Repeat("x", 500) + `"}]}`))
	}))
	defer srv.Close()
	Endpoint = srv.URL
	t.Setenv("TAVILY_API_KEY", "k")
	out, sources := Search("q", 3)
	if !strings.HasPrefix(out, "42\n- Docs <https://www.example.com/a>") || len(out) > 400 {
		t.Fatalf("out = %q", out)
	}
	if len(sources) != 1 || sources[0].Domain() != "example.com" {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestSearchNeedsKey(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "")
	if out, _ := Search("q", 3); !strings.HasPrefix(out, "error:") {
		t.Fatal(out)
	}
}
