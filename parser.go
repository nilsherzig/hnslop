package hnslop

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var detectorScorePattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]{1,3})(?:[^0-9]|$)`)

// DetectorResult contains the fields extracted from one Salahadawi detector
// page. A nil result means that Salahadawi has not analyzed the post.
type DetectorResult struct {
	AIScore *int   `json:"ai_score"`
	URL     string `json:"url"`
}

// PostResponse is the JSON representation exposed by the post endpoints.
// Upstream HTML is parsed before this object is returned.
type PostResponse struct {
	ID             int64           `json:"id"`
	Detector       *DetectorResult `json:"detector"`
	CacheStatus    string          `json:"cache_status"`
	UpstreamStatus int             `json:"upstream_status,omitempty"`
}

// ParseDetectorPage extracts the public result from a Salahadawi detail page.
// The HTML is intentionally parsed here, rather than passed on to API clients.
func ParseDetectorPage(source []byte, upstreamURL string) (*DetectorResult, error) {
	root, err := html.Parse(bytes.NewReader(source))
	if err != nil {
		return nil, err
	}

	if firstElementByClass(root, "art-missing") != nil {
		return nil, nil
	}

	scoreNode := firstElementByClass(root, "art-verdict-big")
	if scoreNode == nil {
		return nil, errors.New("detector page contains no result or missing-result marker")
	}

	return &DetectorResult{
		AIScore: parseDetectorScore(textContent(scoreNode)),
		URL:     upstreamURL,
	}, nil
}

func firstElementByClass(root *html.Node, className string) *html.Node {
	var found *html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode && hasClass(node, className) {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return found
}

func hasClass(node *html.Node, className string) bool {
	for _, class := range strings.Fields(attribute(node, "class")) {
		if class == className {
			return true
		}
	}
	return false
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func textContent(root *html.Node) string {
	var builder strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return builder.String()
}

func parseDetectorScore(value string) *int {
	match := detectorScorePattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return nil
	}
	score, err := strconv.Atoi(match[1])
	if err != nil || score > 100 {
		return nil
	}
	return &score
}
