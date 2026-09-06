package hnslop

import (
	"bufio"
	"errors"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// Version is the version reported by the server and its container.
	Version = "0.5.0"

	defaultDatabasePath   = "data/hnslop.sqlite3"
	defaultUpstreamBase   = "https://www.salahadawi.com"
	defaultPagePath       = "/hacker-news-ai-detector"
	defaultHTTPTimeout    = 20 * time.Second
	envDatabasePath       = "HNSLOP_DATABASE_PATH"
	envUpstreamBase       = "HNSLOP_UPSTREAM_BASE_URL"
	envPagePath           = "HNSLOP_PAGE_PATH"
	envHTTPTimeoutSeconds = "HNSLOP_HTTP_TIMEOUT_SECONDS"
)

// Config contains the settings needed by the HTTP server and upstream proxy.
type Config struct {
	DatabasePath    string
	UpstreamBaseURL string
	PagePath        string
	HTTPTimeout     time.Duration
}

// DefaultConfig returns the configuration used when no environment variables
// are set.
func DefaultConfig() Config {
	return Config{
		DatabasePath:    defaultDatabasePath,
		UpstreamBaseURL: defaultUpstreamBase,
		PagePath:        defaultPagePath,
		HTTPTimeout:     defaultHTTPTimeout,
	}
}

// LoadConfig reads HNSLOP_* variables. A local .env file is supported for
// parity with the previous development setup; real environment variables win.
func LoadConfig() (Config, error) {
	config := DefaultConfig()
	dotEnv, err := readDotEnv(".env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}

	value := func(name, fallback string) string {
		if current, ok := os.LookupEnv(name); ok {
			return current
		}
		if fromFile, ok := dotEnv[name]; ok {
			return fromFile
		}
		return fallback
	}

	config.DatabasePath = value(envDatabasePath, config.DatabasePath)
	config.UpstreamBaseURL = strings.TrimRight(value(envUpstreamBase, config.UpstreamBaseURL), "/")
	config.PagePath = normalizePath(value(envPagePath, config.PagePath))

	timeoutText := value(envHTTPTimeoutSeconds, "20")
	timeoutSeconds, err := strconv.ParseFloat(timeoutText, 64)
	if err != nil || math.IsNaN(timeoutSeconds) || math.IsInf(timeoutSeconds, 0) || timeoutSeconds < 0 {
		return Config{}, errors.New("HNSLOP_HTTP_TIMEOUT_SECONDS must be a non-negative number")
	}
	config.HTTPTimeout = time.Duration(timeoutSeconds * float64(time.Second))

	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Validate checks settings which would otherwise only fail on the first
// request.
func (c Config) Validate() error {
	parsed, err := url.Parse(c.UpstreamBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("HNSLOP_UPSTREAM_BASE_URL must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("HNSLOP_UPSTREAM_BASE_URL must use http or https")
	}
	if c.PagePath == "" || !strings.HasPrefix(c.PagePath, "/") {
		return errors.New("HNSLOP_PAGE_PATH must be an absolute path")
	}
	if c.DatabasePath == "" {
		return errors.New("HNSLOP_DATABASE_PATH must not be empty")
	}
	return nil
}

// PostPath returns the upstream route used for a Hacker News post ID.
func (c Config) PostPath(id int64) string {
	return strings.TrimRight(c.PagePath, "/") + "/" + strconv.FormatInt(id, 10)
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func readDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		values[name] = parseDotEnvValue(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func parseDotEnvValue(value string) string {
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"') {
			return value[1 : len(value)-1]
		}
	}
	if comment, _, ok := strings.Cut(value, " #"); ok {
		return strings.TrimSpace(comment)
	}
	return value
}
