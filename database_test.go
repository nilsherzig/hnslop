package hnslop

import (
	"context"
	"testing"
)

func TestDatabaseRoundTripsOpaqueResponse(t *testing.T) {
	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	want := CachedResponse{
		URL:         "https://example.test/result/1",
		StatusCode:  200,
		ContentType: "text/html; charset=utf-8",
		Body:        []byte("<html>result</html>\x00"),
		FetchedAt:   "2000-01-01T00:00:00Z",
	}
	if err := database.SaveCachedResponse(context.Background(), want); err != nil {
		t.Fatal(err)
	}

	got, err := database.GetCachedResponse(context.Background(), want.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected cached response")
	}
	if string(got.Body) != string(want.Body) || got.URL != want.URL ||
		got.StatusCode != want.StatusCode || got.ContentType != want.ContentType ||
		got.FetchedAt != want.FetchedAt {
		t.Fatalf("cached response mismatch: got %#v, want %#v", *got, want)
	}
}

func TestDatabaseReturnsNilForUnknownURL(t *testing.T) {
	database, err := OpenDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	got, err := database.GetCachedResponse(context.Background(), "https://example.test/missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected no cached response, got %#v", got)
	}
}
