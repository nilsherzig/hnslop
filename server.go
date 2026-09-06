package hnslop

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var indexTemplate = template.Must(template.New("index").Parse(indexPage))

// App exposes the public HTTP API and the embedded example client.
type App struct {
	proxy  *Proxy
	config Config
	logger *log.Logger
}

// NewApp creates an HTTP application around proxy.
func NewApp(proxy *Proxy, config Config, logger *log.Logger) *App {
	if logger == nil {
		logger = log.Default()
	}
	return &App{proxy: proxy, config: config, logger: logger}
}

// Handler returns the complete HTTP handler, including request/result-code
// logging.
func (app *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.HandleFunc("/userscript.js", app.handleUserscript)
	mux.HandleFunc("/assets/hnslop.png", app.handleScreenshot)
	mux.HandleFunc("/v1/posts", app.handlePosts)
	mux.HandleFunc("/v1/posts/", app.handlePost)
	return app.logRequests(mux)
}

func (app *App) handleIndex(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	if !requireMethod(writer, request, http.MethodGet) {
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(writer, struct{ Version string }{Version: Version}); err != nil {
		app.logger.Printf("render index: %v", err)
	}
}

func (app *App) handleHealth(writer http.ResponseWriter, request *http.Request) {
	if !requireMethod(writer, request, http.MethodGet) {
		return
	}
	setAPIHeaders(writer)
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (app *App) handleUserscript(writer http.ResponseWriter, request *http.Request) {
	if !requireMethod(writer, request, http.MethodGet) {
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Content-Disposition", `inline; filename="hnslop-userscript.js"`)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(userscriptAsset)
}

func (app *App) handleScreenshot(writer http.ResponseWriter, request *http.Request) {
	if !requireMethod(writer, request, http.MethodGet) {
		return
	}
	writer.Header().Set("Content-Type", "image/png")
	writer.Header().Set("Content-Disposition", `inline; filename="hnslop.png"`)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(screenshotAsset)
}

func (app *App) handlePosts(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/v1/posts" {
		http.NotFound(writer, request)
		return
	}
	if !requireMethod(writer, request, http.MethodGet) {
		return
	}
	setAPIHeaders(writer)

	force, err := refreshValue(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	ids, err := postIDs(request.URL.Query()["ids"])
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if len(ids) > 100 {
		writeAPIError(writer, http.StatusBadRequest, "at most 100 post IDs may be requested")
		return
	}

	items := make([]batchPost, 0, len(ids))
	hasError := false
	for _, id := range ids {
		result, fetchErr := app.proxy.FetchPost(request.Context(), id, force)
		if fetchErr != nil {
			hasError = true
			app.logProxyResult("batch", id, result, fetchErr)
			items = append(items, batchPost{ID: id, Error: fetchErr.Error()})
			continue
		}
		parsed, parseErr := app.parsePostResponse(id, result)
		if parseErr != nil {
			hasError = true
			app.logProxyResult("batch", id, result, parseErr)
			items = append(items, batchPost{ID: id, Error: parseErr.Error()})
			continue
		}
		app.logProxyResult("batch", id, result, nil)
		items = append(items, batchPostFrom(parsed))
	}

	status := http.StatusOK
	if hasError {
		status = http.StatusBadGateway
	}
	if status == http.StatusOK && !force {
		setPostCacheHeaders(writer)
	}
	writeJSON(writer, status, map[string]any{"posts": items})
}

type batchPost struct {
	ID             int64           `json:"id"`
	Detector       *DetectorResult `json:"detector"`
	CacheStatus    string          `json:"cache_status,omitempty"`
	UpstreamStatus int             `json:"upstream_status,omitempty"`
	Error          string          `json:"error,omitempty"`
}

func (app *App) handlePost(writer http.ResponseWriter, request *http.Request) {
	if !requireMethod(writer, request, http.MethodGet) {
		return
	}
	setAPIHeaders(writer)

	value := strings.TrimPrefix(request.URL.Path, "/v1/posts/")
	if value == "" || strings.Contains(value, "/") {
		writeAPIError(writer, http.StatusBadRequest, "post ID must be a positive integer")
		return
	}
	id, err := ParsePostID(value)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	force, err := refreshValue(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}

	result, err := app.proxy.FetchPost(request.Context(), id, force)
	if err != nil {
		app.logProxyResult("post", id, result, err)
		writeAPIError(writer, http.StatusBadGateway, err.Error())
		return
	}
	parsed, err := app.parsePostResponse(id, result)
	if err != nil {
		app.logProxyResult("post", id, result, err)
		writeAPIError(writer, http.StatusBadGateway, err.Error())
		return
	}
	app.logProxyResult("post", id, result, nil)
	writePostResponse(writer, parsed, !force)
}

func (app *App) parsePostResponse(id int64, result ProxyResult) (PostResponse, error) {
	if result.Response.StatusCode >= http.StatusInternalServerError {
		return PostResponse{}, fmt.Errorf("upstream returned HTTP %d", result.Response.StatusCode)
	}
	detector, err := ParseDetectorPage(result.Response.Body, result.Response.URL)
	if err != nil {
		// Salahadawi normally marks this case with .art-missing, but a plain
		// 404 is also an unambiguous "not analyzed" response.
		if result.Response.StatusCode != http.StatusNotFound {
			return PostResponse{}, fmt.Errorf("parse upstream response: %w", err)
		}
		detector = nil
	}
	upstreamStatus := 0
	if result.CacheStatus == CacheMiss {
		upstreamStatus = result.Response.StatusCode
	}
	return PostResponse{
		ID:             id,
		Detector:       detector,
		CacheStatus:    string(result.CacheStatus),
		UpstreamStatus: upstreamStatus,
	}, nil
}

func batchPostFrom(response PostResponse) batchPost {
	return batchPost{
		ID:             response.ID,
		Detector:       response.Detector,
		CacheStatus:    response.CacheStatus,
		UpstreamStatus: response.UpstreamStatus,
	}
}

func (app *App) logProxyResult(kind string, id int64, result ProxyResult, err error) {
	if err != nil {
		app.logger.Printf("%s id=%d upstream -> error: %v", kind, id, err)
		return
	}
	app.logger.Printf("%s id=%d upstream %s -> %d cache=%s", kind, id, result.Response.URL, result.Response.StatusCode, result.CacheStatus)
}

func (app *App) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder := &statusRecorder{ResponseWriter: writer}
		started := time.Now()
		next.ServeHTTP(recorder, request)
		app.logger.Printf("request %s %s -> %d (%d bytes, %s)", request.Method, request.URL.RequestURI(), recorder.status(), recorder.bytes, time.Since(started).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code  int
	bytes int
}

func (recorder *statusRecorder) WriteHeader(code int) {
	if recorder.code != 0 {
		return
	}
	recorder.code = code
	recorder.ResponseWriter.WriteHeader(code)
}

func (recorder *statusRecorder) Write(body []byte) (int, error) {
	if recorder.code == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	written, err := recorder.ResponseWriter.Write(body)
	recorder.bytes += written
	return written, err
}

func (recorder *statusRecorder) status() int {
	if recorder.code == 0 {
		return http.StatusOK
	}
	return recorder.code
}

func writePostResponse(writer http.ResponseWriter, response PostResponse, cacheable bool) {
	setAPIHeaders(writer)
	if cacheable {
		setPostCacheHeaders(writer)
	}
	writer.Header().Set("X-Hnslop-Cache", response.CacheStatus)
	if response.UpstreamStatus != 0 {
		writer.Header().Set("X-Hnslop-Upstream-Status", strconv.Itoa(response.UpstreamStatus))
	}
	writeJSON(writer, http.StatusOK, response)
}

func setAPIHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Expose-Headers", "Content-Type, X-Hnslop-Cache, X-Hnslop-Upstream-Status")
	writer.Header().Set("Cache-Control", "no-store")
}

func setPostCacheHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "public, max-age=2592000")
}

func requireMethod(writer http.ResponseWriter, request *http.Request, method string) bool {
	if request.Method == method {
		return true
	}
	writer.Header().Set("Allow", method)
	http.Error(writer, "method not allowed\n", http.StatusMethodNotAllowed)
	return false
}

func refreshValue(request *http.Request) (bool, error) {
	value := request.URL.Query().Get("refresh")
	if value == "" {
		return false, nil
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("refresh must be true or false")
	}
}

func postIDs(values []string) ([]int64, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("ids query parameter is required")
	}
	seen := make(map[int64]struct{})
	ids := make([]int64, 0)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			id, err := ParsePostID(part)
			if err != nil {
				return nil, err
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one post ID is required")
	}
	return ids, nil
}

func writeAPIError(writer http.ResponseWriter, status int, message any) {
	writeJSON(writer, status, map[string]any{"error": message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
