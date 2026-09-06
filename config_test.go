package hnslop

import (
	"testing"
	"time"
)

func TestDefaultConfigUsesPermanentCacheSettings(t *testing.T) {
	config := DefaultConfig()
	if config.DatabasePath != "data/hnslop.sqlite3" {
		t.Fatalf("unexpected database path: %s", config.DatabasePath)
	}
	if config.UpstreamBaseURL != "https://www.salahadawi.com" {
		t.Fatalf("unexpected upstream: %s", config.UpstreamBaseURL)
	}
	if config.PagePath != "/hacker-news-ai-detector" {
		t.Fatalf("unexpected page path: %s", config.PagePath)
	}
	if config.HTTPTimeout != 20*time.Second {
		t.Fatalf("unexpected HTTP timeout: %s", config.HTTPTimeout)
	}
	if config.PostPath(42) != "/hacker-news-ai-detector/42" {
		t.Fatalf("unexpected post path: %s", config.PostPath(42))
	}
}

func TestParsePostID(t *testing.T) {
	if id, err := ParsePostID("49582582"); err != nil || id != 49582582 {
		t.Fatalf("could not parse valid ID: %d, %v", id, err)
	}
	for _, value := range []string{"", "0", "-1", "abc", "1/2"} {
		if _, err := ParsePostID(value); err == nil {
			t.Errorf("expected %q to be rejected", value)
		}
	}
}
