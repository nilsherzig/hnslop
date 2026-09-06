package hnslop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CacheStatus describes where a response came from.
type CacheStatus string

const (
	CacheHit    CacheStatus = "hit"
	CacheMiss   CacheStatus = "miss"
	CacheStale  CacheStatus = "stale"
	CacheBypass CacheStatus = "bypass"
)

// ProxyResult contains the unchanged upstream response and cache metadata.
type ProxyResult struct {
	Response    CachedResponse
	CacheStatus CacheStatus
}

// ProxyError is returned when no upstream response is available and no cached
// response can be served.
type ProxyError struct {
	URL string
	Err error
}

func (err *ProxyError) Error() string {
	return fmt.Sprintf("upstream request failed for %s: %v", err.URL, err.Err)
}

func (err *ProxyError) Unwrap() error { return err.Err }

// Proxy fetches Salahadawi responses and stores cacheable responses
// permanently in SQLite. It deliberately treats response bodies as opaque
// bytes for generic FetchPath calls.
type Proxy struct {
	database   *Database
	config     Config
	client     *http.Client
	ownsClient bool

	// A key is only locked while one request for that key is in flight. This
	// avoids duplicate upstream requests when several browser tabs request a
	// new post at the same time without serializing unrelated posts.
	flightMu sync.Mutex
	flights  map[string]*flight
}

type flight struct {
	done chan struct{}
}

// NewProxy creates a proxy. Passing nil uses a client configured with the
// configured upstream timeout.
func NewProxy(database *Database, config Config, client *http.Client) *Proxy {
	ownsClient := false
	if client == nil {
		client = &http.Client{Timeout: config.HTTPTimeout}
		ownsClient = true
	}
	return &Proxy{
		database:   database,
		config:     config,
		client:     client,
		ownsClient: ownsClient,
		flights:    make(map[string]*flight),
	}
}

// Close closes a client created by NewProxy. Injected test clients remain
// owned by their caller.
func (proxy *Proxy) Close() error {
	if proxy.ownsClient && proxy.client != nil {
		proxy.client.CloseIdleConnections()
	}
	return nil
}

type cachePolicy func(CachedResponse) bool

// FetchPost fetches one Hacker News post result by ID. Only detector pages
// containing a result are cached.
func (proxy *Proxy) FetchPost(ctx context.Context, id int64, force bool) (ProxyResult, error) {
	return proxy.fetchPath(ctx, proxy.config.PostPath(id), "", force, detectorResponseCacheable)
}

// FetchPath fetches an arbitrary path below the configured Salahadawi origin.
// The path and query are used as the permanent cache key.
func (proxy *Proxy) FetchPath(ctx context.Context, path, rawQuery string, force bool) (ProxyResult, error) {
	return proxy.fetchPath(ctx, path, rawQuery, force, nil)
}

func (proxy *Proxy) fetchPath(ctx context.Context, path, rawQuery string, force bool, policy cachePolicy) (ProxyResult, error) {
	upstreamURL, err := proxy.upstreamURL(path, rawQuery)
	if err != nil {
		return ProxyResult{}, err
	}

	if !force {
		cached, err := proxy.usableCachedResponse(ctx, upstreamURL, policy)
		if err != nil {
			return ProxyResult{}, err
		}
		if cached != nil {
			return ProxyResult{Response: *cached, CacheStatus: CacheHit}, nil
		}
	}

	release, wait := proxy.acquireFlight(upstreamURL)
	if wait {
		select {
		case <-ctx.Done():
			return ProxyResult{}, ctx.Err()
		case <-release.done:
		}
		if !force {
			cached, err := proxy.usableCachedResponse(ctx, upstreamURL, policy)
			if err != nil {
				return ProxyResult{}, err
			}
			if cached != nil {
				return ProxyResult{Response: *cached, CacheStatus: CacheHit}, nil
			}
		}
	}
	defer proxy.finishFlight(upstreamURL, release)

	// A forced request may have arrived while another request was filling the
	// cache. It intentionally still refreshes, just like refresh=true promised.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return ProxyResult{}, &ProxyError{URL: upstreamURL, Err: err}
	}
	request.Header.Set("User-Agent", "hnslop/"+Version+" (+https://www.salahadawi.com/)")

	response, err := proxy.client.Do(request)
	if err != nil {
		return proxy.cachedAfterFailure(ctx, upstreamURL, err, policy)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return proxy.cachedAfterFailure(ctx, upstreamURL, readErr, policy)
	}
	if closeErr != nil {
		return proxy.cachedAfterFailure(ctx, upstreamURL, closeErr, policy)
	}

	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	cachedResponse := CachedResponse{
		URL:         upstreamURL,
		StatusCode:  response.StatusCode,
		ContentType: contentType,
		Body:        body,
		FetchedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}

	// Only cache successful detector pages for post requests. A missing-result
	// page may be represented either by HTTP 404 or by an .art-missing marker.
	// 5xx responses are never cached and may fall back to a valid stale entry.
	if response.StatusCode < http.StatusInternalServerError && cacheableResponse(cachedResponse, policy) {
		if err := proxy.database.SaveCachedResponse(ctx, cachedResponse); err != nil {
			return ProxyResult{}, err
		}
		return ProxyResult{Response: cachedResponse, CacheStatus: CacheMiss}, nil
	}

	if response.StatusCode >= http.StatusInternalServerError {
		if cached, err := proxy.usableCachedResponse(ctx, upstreamURL, policy); err != nil {
			return ProxyResult{}, err
		} else if cached != nil {
			return ProxyResult{Response: *cached, CacheStatus: CacheStale}, nil
		}
	}
	return ProxyResult{Response: cachedResponse, CacheStatus: CacheBypass}, nil
}

func (proxy *Proxy) cachedAfterFailure(ctx context.Context, upstreamURL string, cause error, policy cachePolicy) (ProxyResult, error) {
	cached, err := proxy.usableCachedResponse(ctx, upstreamURL, policy)
	if err != nil {
		return ProxyResult{}, err
	}
	if cached != nil {
		return ProxyResult{Response: *cached, CacheStatus: CacheStale}, nil
	}
	return ProxyResult{}, &ProxyError{URL: upstreamURL, Err: cause}
}

func (proxy *Proxy) usableCachedResponse(ctx context.Context, upstreamURL string, policy cachePolicy) (*CachedResponse, error) {
	cached, err := proxy.database.GetCachedResponse(ctx, upstreamURL)
	if err != nil {
		return nil, err
	}
	if cached == nil || cacheableResponse(*cached, policy) {
		return cached, nil
	}
	if err := proxy.database.DeleteCachedResponse(ctx, upstreamURL); err != nil {
		return nil, err
	}
	return nil, nil
}

func cacheableResponse(response CachedResponse, policy cachePolicy) bool {
	return policy == nil || policy(response)
}

func detectorResponseCacheable(response CachedResponse) bool {
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return false
	}
	detector, err := ParseDetectorPage(response.Body, response.URL)
	return err == nil && detector != nil && detector.AIScore != nil
}

func (proxy *Proxy) upstreamURL(path, rawQuery string) (string, error) {
	base, err := url.Parse(proxy.config.UpstreamBaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		if err == nil {
			err = errors.New("upstream base URL is not absolute")
		}
		return "", err
	}
	path = "/" + strings.TrimLeft(path, "/")
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawPath = ""
	base.RawQuery = rawQuery
	base.Fragment = ""
	return base.String(), nil
}

func (proxy *Proxy) acquireFlight(key string) (*flight, bool) {
	proxy.flightMu.Lock()
	defer proxy.flightMu.Unlock()
	if existing, ok := proxy.flights[key]; ok {
		return existing, true
	}
	current := &flight{done: make(chan struct{})}
	proxy.flights[key] = current
	return current, false
}

func (proxy *Proxy) finishFlight(key string, current *flight) {
	proxy.flightMu.Lock()
	if proxy.flights[key] == current {
		delete(proxy.flights, key)
		close(current.done)
	}
	proxy.flightMu.Unlock()
}

// ParsePostID validates the path component used by /v1/posts/{id}.
func ParsePostID(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "-") {
		return 0, errors.New("post ID must be a positive integer")
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("post ID must be a positive integer")
	}
	return id, nil
}
