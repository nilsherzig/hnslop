package hnslop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func testProxy(t *testing.T, handler http.Handler) (*Proxy, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)

	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	config := DefaultConfig()
	config.UpstreamBaseURL = upstream.URL
	proxy := NewProxy(database, config, upstream.Client())
	return proxy, upstream
}

func TestProxyCachesResponseIndefinitely(t *testing.T) {
	var calls atomic.Int32
	proxy, _ := testProxy(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/hacker-news-ai-detector/42" {
			t.Errorf("unexpected upstream path: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte("<html>result</html>"))
	}))

	first, err := proxy.FetchPost(context.Background(), 42, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proxy.FetchPost(context.Background(), 42, false)
	if err != nil {
		t.Fatal(err)
	}

	if first.CacheStatus != CacheMiss || second.CacheStatus != CacheHit {
		t.Fatalf("unexpected cache statuses: %s, %s", first.CacheStatus, second.CacheStatus)
	}
	if string(second.Response.Body) != "<html>result</html>" {
		t.Fatalf("unexpected cached body: %q", second.Response.Body)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one upstream request, got %d", calls.Load())
	}
}

func TestProxyDoesNotExpireOldCacheEntries(t *testing.T) {
	var calls atomic.Int32
	proxy, upstream := testProxy(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte("upstream should not be called"))
	}))

	url := upstream.URL + "/hacker-news-ai-detector/8"
	if err := proxy.database.SaveCachedResponse(context.Background(), CachedResponse{
		URL:         url,
		StatusCode:  200,
		ContentType: "text/html",
		Body:        []byte("permanent result"),
		FetchedAt:   "1970-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := proxy.FetchPost(context.Background(), 8, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.CacheStatus != CacheHit || string(result.Response.Body) != "permanent result" {
		t.Fatalf("unexpected old cache result: %#v", result)
	}
	if calls.Load() != 0 {
		t.Fatalf("old cache entry was refreshed unexpectedly: %d calls", calls.Load())
	}
}

func TestProxyUsesCachedResponseWhenRefreshFails(t *testing.T) {
	var calls atomic.Int32
	proxy, upstream := testProxy(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = writer.Write([]byte("cached"))
			return
		}
		http.Error(writer, "upstream unavailable", http.StatusBadGateway)
	}))

	first, err := proxy.FetchPost(context.Background(), 7, false)
	if err != nil {
		t.Fatal(err)
	}
	upstream.Close()
	second, err := proxy.FetchPost(context.Background(), 7, true)
	if err != nil {
		t.Fatal(err)
	}

	if first.CacheStatus != CacheMiss || second.CacheStatus != CacheStale {
		t.Fatalf("unexpected cache statuses: %s, %s", first.CacheStatus, second.CacheStatus)
	}
	if string(second.Response.Body) != "cached" {
		t.Fatalf("unexpected stale body: %q", second.Response.Body)
	}
}

func TestProxyCachesNotFoundResponses(t *testing.T) {
	var calls atomic.Int32
	proxy, _ := testProxy(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		http.Error(writer, "not found", http.StatusNotFound)
	}))

	first, err := proxy.FetchPost(context.Background(), 99, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proxy.FetchPost(context.Background(), 99, false)
	if err != nil {
		t.Fatal(err)
	}

	if first.Response.StatusCode != http.StatusNotFound || second.Response.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status codes: %d, %d", first.Response.StatusCode, second.Response.StatusCode)
	}
	if second.CacheStatus != CacheHit || calls.Load() != 1 {
		t.Fatalf("expected cached not-found response, status=%s calls=%d", second.CacheStatus, calls.Load())
	}
}
