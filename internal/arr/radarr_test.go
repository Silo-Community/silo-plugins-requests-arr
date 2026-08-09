package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmitMovieAddsLookupResult(t *testing.T) {
	qualityProfileID := 7
	var posted movieResource
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "radarr-key" {
			t.Fatalf("X-Api-Key = %q, want radarr-key", got)
		}
		switch r.URL.Path {
		case "/api/v3/movie":
			if r.Method == http.MethodGet {
				if got := r.URL.Query().Get("tmdbId"); got != "550" {
					t.Fatalf("tmdbId = %q, want 550", got)
				}
				w.Write([]byte(`[]`))
				return
			}
			if r.Method == http.MethodPost {
				if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
					t.Fatalf("decode posted movie: %v", err)
				}
				w.Write([]byte(`{"id":42,"tmdbId":550}`))
				return
			}
		case "/api/v3/movie/lookup/tmdb":
			if got := r.URL.Query().Get("tmdbId"); got != "550" {
				t.Fatalf("lookup tmdbId = %q, want 550", got)
			}
			w.Write([]byte(`{"title":"Fight Club","tmdbId":550,"titleSlug":"fight-club"}`))
			return
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	result, err := client.SubmitMovie(context.Background(), Request{
		MediaType: MediaTypeMovie,
		TMDBID:    550,
		Title:     "Fight Club",
	}, Instance{
		Kind:             "radarr",
		BaseURL:          server.URL,
		APIKeyRef:        "radarr-key",
		RootFolder:       "/movies",
		QualityProfileID: &qualityProfileID,
		Options: map[string]any{
			"search_on_add": false,
		},
	})
	if err != nil {
		t.Fatalf("SubmitMovie returned error: %v", err)
	}
	if result.ExternalID != "42" || result.IntegrationKind != "radarr" {
		t.Fatalf("result = %+v, want radarr external id 42", result)
	}
	if posted.RootFolderPath != "/movies" || posted.QualityProfileID != qualityProfileID {
		t.Fatalf("posted movie = %+v, missing root folder/quality profile", posted)
	}
	if posted.AddOptions.SearchForMovie {
		t.Fatalf("searchForMovie = true, want false")
	}
}

func TestSubmitMovieAdoptsExistingMovie(t *testing.T) {
	qualityProfileID := 7
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/movie" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("tmdbId"); got != "550" {
			t.Fatalf("tmdbId = %q, want 550", got)
		}
		w.Write([]byte(`[{"id":42,"tmdbId":550,"qualityProfileId":99,"rootFolderPath":"/existing"}]`))
	}))
	defer server.Close()

	result, err := NewRadarrClient(server.Client()).SubmitMovie(context.Background(), Request{
		MediaType: MediaTypeMovie,
		TMDBID:    550,
	}, Instance{
		Kind:             "radarr",
		BaseURL:          server.URL,
		APIKeyRef:        "radarr-key",
		RootFolder:       "/movies",
		QualityProfileID: &qualityProfileID,
	})
	if err != nil {
		t.Fatalf("SubmitMovie returned error: %v", err)
	}
	if result.ExternalID != "42" || result.ExternalStatus != "queued" {
		t.Fatalf("result = %+v, want adopted Radarr movie 42", result)
	}
}

func TestSubmitMovieFailsWhenPreflightFails(t *testing.T) {
	qualityProfileID := 7
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/movie" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := NewRadarrClient(server.Client()).SubmitMovie(context.Background(), Request{
		MediaType: MediaTypeMovie,
		TMDBID:    550,
	}, Instance{
		Kind:             "radarr",
		BaseURL:          server.URL,
		APIKeyRef:        "radarr-key",
		QualityProfileID: &qualityProfileID,
	})
	if err == nil {
		t.Fatal("expected preflight error")
	}
}

func TestSubmitMovieRecoversWhenConcurrentAddWins(t *testing.T) {
	qualityProfileID := 7
	lookupCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie":
			if r.Method == http.MethodGet {
				lookupCount++
				if lookupCount == 1 {
					w.Write([]byte(`[]`))
					return
				}
				w.Write([]byte(`[{"id":42,"tmdbId":550}]`))
				return
			}
			http.Error(w, "already added", http.StatusBadRequest)
		case "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Fight Club","tmdbId":550,"titleSlug":"fight-club"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	result, err := NewRadarrClient(server.Client()).SubmitMovie(context.Background(), Request{
		MediaType: MediaTypeMovie,
		TMDBID:    550,
	}, Instance{
		Kind:             "radarr",
		BaseURL:          server.URL,
		APIKeyRef:        "radarr-key",
		RootFolder:       "/movies",
		QualityProfileID: &qualityProfileID,
	})
	if err != nil {
		t.Fatalf("SubmitMovie returned error: %v", err)
	}
	if result.ExternalID != "42" || lookupCount != 2 {
		t.Fatalf("result = %+v, lookups = %d; want movie 42 after two lookups", result, lookupCount)
	}
}

func TestSubmitMoviePreservesPostErrorWhenRecoveryFindsNothing(t *testing.T) {
	qualityProfileID := 7
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie":
			if r.Method == http.MethodGet {
				w.Write([]byte(`[]`))
				return
			}
			http.Error(w, "add failed", http.StatusInternalServerError)
		case "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Fight Club","tmdbId":550,"titleSlug":"fight-club"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	_, err := NewRadarrClient(server.Client()).SubmitMovie(context.Background(), Request{
		MediaType: MediaTypeMovie,
		TMDBID:    550,
	}, Instance{
		Kind:             "radarr",
		BaseURL:          server.URL,
		APIKeyRef:        "radarr-key",
		RootFolder:       "/movies",
		QualityProfileID: &qualityProfileID,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("error = %v, want original HTTP 500 error", err)
	}
}

func TestSubmitMovieRecoversFromEmptyAddResponse(t *testing.T) {
	qualityProfileID := 7
	lookupCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Fight Club","tmdbId":550,"titleSlug":"fight-club"}`))
		case "/api/v3/movie":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				return
			}
			if r.Method == http.MethodGet {
				lookupCount++
				if got := r.URL.Query().Get("tmdbId"); got != "550" {
					t.Fatalf("tmdbId = %q, want 550", got)
				}
				if lookupCount == 1 {
					w.Write([]byte(`[]`))
					return
				}
				w.Write([]byte(`[{"id":99,"tmdbId":550,"title":"Fight Club"}]`))
				return
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	result, err := client.SubmitMovie(context.Background(), Request{
		MediaType: MediaTypeMovie,
		TMDBID:    550,
		Title:     "Fight Club",
	}, Instance{
		Kind:             "radarr",
		BaseURL:          server.URL,
		APIKeyRef:        "radarr-key",
		RootFolder:       "/movies",
		QualityProfileID: &qualityProfileID,
	})
	if err != nil {
		t.Fatalf("SubmitMovie returned error: %v", err)
	}
	if result.ExternalID != "99" {
		t.Fatalf("ExternalID = %q, want 99 (recovered after empty 201)", result.ExternalID)
	}
	if result.ExternalStatus != "queued" {
		t.Fatalf("ExternalStatus = %q, want queued", result.ExternalStatus)
	}
}

func TestSubmitMovieFallsBackWhenEmptyResponseAndLookupFails(t *testing.T) {
	qualityProfileID := 7
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Fight Club","tmdbId":550,"titleSlug":"fight-club"}`))
		case "/api/v3/movie":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				return
			}
			if r.Method == http.MethodGet {
				w.Write([]byte(`[]`))
				return
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	result, err := client.SubmitMovie(context.Background(), Request{
		MediaType: MediaTypeMovie,
		TMDBID:    550,
		Title:     "Fight Club",
	}, Instance{
		Kind:             "radarr",
		BaseURL:          server.URL,
		APIKeyRef:        "radarr-key",
		RootFolder:       "/movies",
		QualityProfileID: &qualityProfileID,
	})
	if err != nil {
		t.Fatalf("SubmitMovie returned error: %v", err)
	}
	if result.ExternalStatus != "accepted_without_response" {
		t.Fatalf("ExternalStatus = %q, want accepted_without_response", result.ExternalStatus)
	}
}

func TestCheckMovieStatusReadsQueueDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "radarr-key" {
			t.Fatalf("X-Api-Key = %q, want radarr-key", got)
		}
		if r.URL.Path != "/api/v3/queue/details" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("movieId"); got != "42" {
			t.Fatalf("movieId = %q, want 42", got)
		}
		w.Write([]byte(`[{"movieId":42,"status":"downloading","trackedDownloadState":"downloading"}]`))
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	status, err := client.CheckMovieStatus(context.Background(), Request{
		MediaType:  MediaTypeMovie,
		TMDBID:     550,
		ExternalID: "42",
	}, Instance{
		Kind:      "radarr",
		BaseURL:   server.URL,
		APIKeyRef: "radarr-key",
	})
	if err != nil {
		t.Fatalf("CheckMovieStatus returned error: %v", err)
	}
	if status.Status != StatusDownloading || status.ExternalStatus != "downloading/downloading" {
		t.Fatalf("status = %+v, want downloading", status)
	}
}

// An imported movie is gone from the queue, so an empty queue plus hasFile is
// the only signal that the request was actually fulfilled.
func TestCheckMovieStatusCompletesWhenImported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue/details":
			w.Write([]byte(`[]`))
		case "/api/v3/movie/42":
			w.Write([]byte(`{"id":42,"hasFile":true}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	status, err := client.CheckMovieStatus(context.Background(), Request{
		MediaType:  MediaTypeMovie,
		TMDBID:     550,
		ExternalID: "42",
	}, Instance{
		Kind:      "radarr",
		BaseURL:   server.URL,
		APIKeyRef: "radarr-key",
	})
	if err != nil {
		t.Fatalf("CheckMovieStatus returned error: %v", err)
	}
	if status.Status != StatusCompleted || status.ExternalStatus != "imported" {
		t.Fatalf("status = %+v, want completed/imported", status)
	}
	if status.ExternalID != "42" {
		t.Fatalf("ExternalID = %q, want 42", status.ExternalID)
	}
}

// An empty queue with no file means nothing was grabbed yet — still queued.
func TestCheckMovieStatusStaysQueuedWithoutFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue/details":
			w.Write([]byte(`[]`))
		case "/api/v3/movie/42":
			w.Write([]byte(`{"id":42,"hasFile":false}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	status, err := client.CheckMovieStatus(context.Background(), Request{
		MediaType:  MediaTypeMovie,
		TMDBID:     550,
		ExternalID: "42",
	}, Instance{Kind: "radarr", BaseURL: server.URL, APIKeyRef: "radarr-key"})
	if err != nil {
		t.Fatalf("CheckMovieStatus returned error: %v", err)
	}
	if status.Status != StatusQueued || status.ExternalStatus != "not_in_queue" {
		t.Fatalf("status = %+v, want queued/not_in_queue", status)
	}
}

// A non-empty queue is authoritative; the movie record is not fetched at all.
func TestCheckMovieStatusSkipsMovieLookupWhileQueued(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/queue/details" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`[{"movieId":42,"status":"queued"}]`))
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	status, err := client.CheckMovieStatus(context.Background(), Request{
		MediaType:  MediaTypeMovie,
		TMDBID:     550,
		ExternalID: "42",
	}, Instance{Kind: "radarr", BaseURL: server.URL, APIKeyRef: "radarr-key"})
	if err != nil {
		t.Fatalf("CheckMovieStatus returned error: %v", err)
	}
	if status.Status != StatusQueued {
		t.Fatalf("status = %+v, want queued", status)
	}
}

func TestListMovieIntegrationOptionsLoadsChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "radarr-key" {
			t.Fatalf("X-Api-Key = %q, want radarr-key", got)
		}
		switch r.URL.Path {
		case "/api/v3/rootfolder":
			w.Write([]byte(`[{"path":"/movies","freeSpace":123,"totalSpace":456,"accessible":true}]`))
		case "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":7,"name":"HD-1080p"}]`))
		case "/api/v3/tag":
			w.Write([]byte(`[{"id":2,"label":"requests"}]`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewRadarrClient(server.Client())
	options, err := client.ListMovieIntegrationOptions(context.Background(), Instance{
		Kind:      "radarr",
		BaseURL:   server.URL,
		APIKeyRef: "radarr-key",
	})
	if err != nil {
		t.Fatalf("ListMovieIntegrationOptions returned error: %v", err)
	}
	if options.Kind != "radarr" {
		t.Fatalf("kind = %q, want radarr", options.Kind)
	}
	if len(options.RootFolders) != 1 || options.RootFolders[0].Path != "/movies" {
		t.Fatalf("root folders = %+v, want /movies", options.RootFolders)
	}
	if len(options.QualityProfiles) != 1 || options.QualityProfiles[0].ID != 7 {
		t.Fatalf("quality profiles = %+v, want id 7", options.QualityProfiles)
	}
	if len(options.Tags) != 1 || options.Tags[0].ID != 2 {
		t.Fatalf("tags = %+v, want id 2", options.Tags)
	}
}
