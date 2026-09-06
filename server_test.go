package hnslop

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIndexServesEmbeddedHTML(t *testing.T) {
	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	config := DefaultConfig()
	proxy := NewProxy(database, config, nil)
	app := NewApp(proxy, config, log.New(io.Discard, "", 0))

	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected index status: %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("unexpected index content type: %s", response.Header().Get("Content-Type"))
	}
	if !strings.Contains(strings.ToLower(response.Body.String()), "<html") {
		t.Fatalf("index did not serve HTML: %s", response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `src="/assets/hnslop.png"`) {
		t.Fatalf("index did not link the screenshot: %s", body)
	}
	for _, example := range []string{
		"curl 'https://hnslop.nilsherzig.com/v1/posts?ids=49582582,49541888' | jq",
		"curl 'https://hnslop.nilsherzig.com/v1/posts/49582582' | jq",
	} {
		if !strings.Contains(body, example) {
			t.Fatalf("index did not use the public API host in %q: %s", example, body)
		}
	}
	if strings.Contains(body, "127.0.0.1:8000") {
		t.Fatalf("index still contains the local default host: %s", body)
	}
}

func TestScreenshotIsServedAsPNG(t *testing.T) {
	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	config := DefaultConfig()
	proxy := NewProxy(database, config, nil)
	app := NewApp(proxy, config, log.New(io.Discard, "", 0))

	request := httptest.NewRequest(http.MethodGet, "http://example.test/assets/hnslop.png", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected screenshot status: %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("unexpected screenshot content type: %s", response.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(response.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("screenshot response was not a PNG")
	}
}

func TestUserscriptIsServedAsPlainText(t *testing.T) {
	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	config := DefaultConfig()
	proxy := NewProxy(database, config, nil)
	app := NewApp(proxy, config, log.New(io.Discard, "", 0))

	request := httptest.NewRequest(http.MethodGet, "http://example.test/userscript.js", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected userscript status: %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected userscript content type: %s", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Content-Disposition") != `inline; filename="hnslop-userscript.js"` {
		t.Fatalf("unexpected userscript disposition: %s", response.Header().Get("Content-Disposition"))
	}
	body := response.Body.String()
	if !strings.Contains(body, "// ==UserScript==") {
		t.Fatal("userscript body was not served as text")
	}
	if !strings.Contains(body, `@connect      hnslop.nilsherzig.com`) ||
		!strings.Contains(body, `const API_BASE_URL = "https://hnslop.nilsherzig.com"`) {
		t.Fatalf("userscript did not use the public API host: %s", body)
	}
}

func TestUpstreamHTMLRoutesAreNotExposed(t *testing.T) {
	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	config := DefaultConfig()
	proxy := NewProxy(database, config, nil)
	app := NewApp(proxy, config, log.New(io.Discard, "", 0))
	for _, path := range []string{"/v1/page", "/hacker-news-ai-detector/49582582"} {
		request := httptest.NewRequest(http.MethodGet, "http://example.test"+path, nil)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("expected %s to be unavailable, got %d", path, response.Code)
		}
	}
}

func TestPostEndpointReturnsParsedAndCachesUpstreamResponse(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/hacker-news-ai-detector/123" {
			t.Errorf("unexpected upstream path: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "text/html")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`<html><body>
			<div class="art-verdict-big v-mixed"> 33 <span>%</span></div>
			<span class="art-dark-badge v-mixed">Mixed</span>
		</body></html>`))
	}))
	defer upstream.Close()

	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	config := DefaultConfig()
	config.UpstreamBaseURL = upstream.URL
	proxy := NewProxy(database, config, upstream.Client())
	app := NewApp(proxy, config, log.New(io.Discard, "", 0))
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	first := get(t, server.URL+"/v1/posts/123")
	second := get(t, server.URL+"/v1/posts/123")

	for _, response := range []*http.Response{first, second} {
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		var payload PostResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("response was not JSON: %v; body=%q", err, body)
		}
		if response.StatusCode != http.StatusOK || payload.ID != 123 || payload.Detector == nil {
			t.Fatalf("unexpected response: status=%d payload=%#v", response.StatusCode, payload)
		}
		if payload.Detector.AIScore == nil || *payload.Detector.AIScore != 33 {
			t.Fatalf("unexpected detector result: %#v", payload.Detector)
		}
		if strings.Contains(string(body), "<html>") {
			t.Fatalf("response leaked upstream HTML: %s", body)
		}
		if strings.Contains(string(body), `"upstream_url"`) {
			t.Fatalf("response duplicated the detector URL as upstream_url: %s", body)
		}
		if strings.Contains(string(body), `"verdict"`) {
			t.Fatalf("response exposed the redundant verdict: %s", body)
		}
		if response.Header.Get("Cache-Control") != "public, max-age=2592000" {
			t.Fatalf("unexpected browser cache policy: %s", response.Header.Get("Cache-Control"))
		}
		if response.Header.Get("X-Hnslop-Cache") == string(CacheHit) {
			if payload.UpstreamStatus != 0 || strings.Contains(string(body), `"upstream_status"`) {
				t.Fatalf("cache hit exposed an upstream status: %s", body)
			}
		} else if payload.UpstreamStatus != http.StatusOK {
			t.Fatalf("cache miss omitted the upstream status: %s", body)
		}
	}
	if first.Header.Get("X-Hnslop-Cache") != string(CacheMiss) {
		t.Fatalf("first request was not a cache miss: %s", first.Header.Get("X-Hnslop-Cache"))
	}
	if second.Header.Get("X-Hnslop-Cache") != string(CacheHit) {
		t.Fatalf("second request was not a cache hit: %s", second.Header.Get("X-Hnslop-Cache"))
	}
	if first.Header.Get("X-Hnslop-Upstream-Status") != "200" {
		t.Fatalf("missing upstream status header: %s", first.Header.Get("X-Hnslop-Upstream-Status"))
	}
	if second.Header.Get("X-Hnslop-Upstream-Status") != "" {
		t.Fatalf("cache hit exposed an upstream status header: %s", second.Header.Get("X-Hnslop-Upstream-Status"))
	}
	if first.Header.Get("X-Hnslop-Upstream-URL") != "" {
		t.Fatalf("response still exposed a duplicate upstream URL header: %s", first.Header.Get("X-Hnslop-Upstream-URL"))
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one upstream request, got %d", calls.Load())
	}

	refreshed := get(t, server.URL+"/v1/posts/123?refresh=true")
	_, _ = io.Copy(io.Discard, refreshed.Body)
	_ = refreshed.Body.Close()
	if refreshed.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("refresh response was cacheable: %s", refreshed.Header.Get("Cache-Control"))
	}
	if calls.Load() != 2 {
		t.Fatalf("refresh did not request upstream: %d calls", calls.Load())
	}
}

func TestPostEndpointReturnsJSONForMissingPost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "not found", http.StatusNotFound)
	}))
	defer upstream.Close()

	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	config := DefaultConfig()
	config.UpstreamBaseURL = upstream.URL
	proxy := NewProxy(database, config, upstream.Client())
	app := NewApp(proxy, config, log.New(io.Discard, "", 0))

	request := httptest.NewRequest(http.MethodGet, "http://example.test/v1/posts/99", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d: %s", response.Code, response.Body.String())
	}

	var payload PostResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != 99 || payload.Detector != nil || payload.UpstreamStatus != http.StatusNotFound ||
		payload.CacheStatus != string(CacheBypass) {
		t.Fatalf("unexpected missing-post payload: %#v", payload)
	}
	if response.Header().Get("X-Hnslop-Cache") != string(CacheBypass) {
		t.Fatalf("missing post was reported as cached: %s", response.Header().Get("X-Hnslop-Cache"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing post response was cacheable: %s", response.Header().Get("Cache-Control"))
	}
	if strings.Contains(response.Body.String(), "not found") {
		t.Fatalf("response leaked upstream error page: %s", response.Body.String())
	}
}

func TestBatchEndpointReturnsParsedResults(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/hacker-news-ai-detector/2" {
			_, _ = writer.Write([]byte(`<div class="art-missing">No analysis found</div>`))
			return
		}
		_, _ = writer.Write([]byte(`<div class="art-verdict-big v-human"> 12 <span>%</span></div><span class="art-dark-badge v-human">Human</span>`))
	}))
	defer upstream.Close()

	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	config := DefaultConfig()
	config.UpstreamBaseURL = upstream.URL
	proxy := NewProxy(database, config, upstream.Client())
	app := NewApp(proxy, config, log.New(io.Discard, "", 0))

	request := httptest.NewRequest(http.MethodGet, "http://example.test/v1/posts?ids=1,2", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected batch status: %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected batch browser cache policy: %s", response.Header().Get("Cache-Control"))
	}

	var payload struct {
		Posts []batchPost `json:"posts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Posts) != 2 || payload.Posts[0].ID != 1 || payload.Posts[1].ID != 2 {
		t.Fatalf("unexpected batch payload: %#v", payload.Posts)
	}
	if payload.Posts[0].UpstreamStatus != http.StatusOK || payload.Posts[0].Detector == nil ||
		payload.Posts[0].Detector.AIScore == nil || *payload.Posts[0].Detector.AIScore != 12 {
		t.Fatalf("missing parsed response in batch payload: %#v", payload.Posts[0])
	}
	if payload.Posts[1].Detector != nil || payload.Posts[1].CacheStatus != string(CacheBypass) ||
		payload.Posts[1].UpstreamStatus != http.StatusOK {
		t.Fatalf("expected an uncached missing detector result, got %#v", payload.Posts[1])
	}
	if strings.Contains(response.Body.String(), "body_base64") || strings.Contains(response.Body.String(), "<div") {
		t.Fatalf("batch response leaked upstream HTML: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"upstream_url"`) {
		t.Fatalf("batch response duplicated the detector URL as upstream_url: %s", response.Body.String())
	}

	hitRequest := httptest.NewRequest(http.MethodGet, "http://example.test/v1/posts?ids=1,2", nil)
	hitResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(hitResponse, hitRequest)
	var hitPayload struct {
		Posts []batchPost `json:"posts"`
	}
	if err := json.Unmarshal(hitResponse.Body.Bytes(), &hitPayload); err != nil {
		t.Fatal(err)
	}
	if hitResponse.Code != http.StatusOK || len(hitPayload.Posts) != 2 {
		t.Fatalf("unexpected cached batch response: status=%d payload=%#v", hitResponse.Code, hitPayload)
	}
	if hitResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected cached batch browser cache policy: %s", hitResponse.Header().Get("Cache-Control"))
	}
	if hitPayload.Posts[0].CacheStatus != string(CacheHit) || hitPayload.Posts[0].UpstreamStatus != 0 {
		t.Fatalf("cached batch response exposed upstream status: %#v", hitPayload.Posts[0])
	}
	if hitPayload.Posts[1].CacheStatus != string(CacheBypass) || hitPayload.Posts[1].UpstreamStatus != http.StatusOK {
		t.Fatalf("missing batch response was unexpectedly cached: %#v", hitPayload.Posts[1])
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
