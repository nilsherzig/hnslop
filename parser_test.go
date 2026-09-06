package hnslop

import (
	"strings"
	"testing"
)

func TestParseDetectorPageReturnsScore(t *testing.T) {
	source := []byte(`
<html><body>
  <h1 class="art-title">GPT-6 Astra on robotic manipulation</h1>
  <div class="art-verdict-card">
    <div class="art-verdict-big hn-tnum v-mixed"> 33 <span>%</span></div>
    <span class="art-dark-badge v-mixed"><span class="dot"></span>Mixed</span>
  </div>
</body></html>`)

	result, err := ParseDetectorPage(source, "https://example.test/hacker-news-ai-detector/42")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected a detector result")
	}
	if result.AIScore == nil || *result.AIScore != 33 {
		t.Fatalf("unexpected score: %#v", result.AIScore)
	}
	if result.URL != "https://example.test/hacker-news-ai-detector/42" {
		t.Fatalf("unexpected URL: %q", result.URL)
	}
}

func TestParseDetectorPageReturnsNilForMissingPost(t *testing.T) {
	source := []byte(`<html><body><div class="art-missing">No analysis found</div></body></html>`)

	result, err := ParseDetectorPage(source, "https://example.test/hacker-news-ai-detector/99")
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected no detector result, got %#v", result)
	}
}

func TestParseDetectorPageRejectsUnknownPage(t *testing.T) {
	_, err := ParseDetectorPage([]byte(`<html><body>unexpected response</body></html>`), "https://example.test/42")
	if err == nil || !strings.Contains(err.Error(), "no result") {
		t.Fatalf("expected unknown-page error, got %v", err)
	}
}
