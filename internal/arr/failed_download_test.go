package arr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// failedDownloadStub is a Radarr or Sonarr whose queue holds the given items.
// It answers the download client settings with config, or fails the read when
// config is empty, and counts how often they are read.
type failedDownloadStub struct {
	queue       string
	config      string
	configReads atomic.Int32
}

func (s *failedDownloadStub) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue/details":
			_, _ = w.Write([]byte(s.queue))
		case "/api/v3/config/downloadclient":
			s.configReads.Add(1)
			if s.config == "" {
				http.Error(w, "database is locked", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(s.config))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
}

// checkKind checks one target of a service kind against a stub server.
type checkKind struct {
	kind  string
	label string
	check func(ctx context.Context, server *httptest.Server) (FulfillmentStatus, error)
}

var checkKinds = []checkKind{
	{"radarr", "Radarr", func(ctx context.Context, server *httptest.Server) (FulfillmentStatus, error) {
		return NewRadarrClient(server.Client()).CheckMovieStatus(ctx, Request{MediaType: MediaTypeMovie, ExternalID: "42"},
			Instance{Kind: "radarr", BaseURL: server.URL, APIKeyRef: "k"})
	}},
	{"sonarr", "Sonarr", func(ctx context.Context, server *httptest.Server) (FulfillmentStatus, error) {
		return NewSonarrClient(server.Client()).CheckSeriesStatus(ctx, Request{MediaType: MediaTypeSeries, ExternalID: "42"},
			Instance{Kind: "sonarr", BaseURL: server.URL, APIKeyRef: "k"})
	}},
}

// A failed download fails its target only when the service will not look for
// another release. Radarr and Sonarr search again when "Redownload Failed" is
// on, which is their default, so the target stays queued. A setting that
// cannot be read counts as on: a transient error must not fail a request the
// service may still recover.
func TestCheckStatusReportsFailedDownloadTheServiceWillNotRetry(t *testing.T) {
	// A download the client finished but Radarr or Sonarr found broken, as
	// both report it before failed download handling removes it.
	const queue = `[{"id":9,"movieId":42,"seriesId":42,"title":"Title.2026.1080p","status":"completed",
		"trackedDownloadStatus":"error","trackedDownloadState":"failedPending",
		"statusMessages":[{"title":"Title.2026.1080p","messages":["Download failed"]}],
		"errorMessage":"Unpacking failed, write error or disk is full?","downloadId":"abc","size":2000.0,"sizeleft":0.0}]`
	const failure = "external queue failed for Title.2026.1080p: completed/failedPending"
	cases := []struct {
		name       string
		config     string
		redownload bool
	}{
		{"redownload on", `{"enableCompletedDownloadHandling":true,"autoRedownloadFailed":true,"autoRedownloadFailedFromInteractiveSearch":true,"id":1}`, true},
		{"redownload off", `{"enableCompletedDownloadHandling":true,"autoRedownloadFailed":false,"autoRedownloadFailedFromInteractiveSearch":true,"id":1}`, false},
		{"setting absent", `{"id":1}`, true},
		{"settings unreadable", "", true},
	}
	for _, kind := range checkKinds {
		for _, tc := range cases {
			t.Run(kind.kind+"/"+tc.name, func(t *testing.T) {
				stub := &failedDownloadStub{queue: queue, config: tc.config}
				server := stub.serve(t)
				defer server.Close()

				got, err := kind.check(context.Background(), server)
				if err != nil {
					t.Fatalf("check returned error: %v", err)
				}
				wantStatus, wantMessage := StatusFailed, failure
				if tc.redownload {
					wantStatus, wantMessage = StatusQueued, failure+"; "+kind.label+" is looking for another release"
				}
				if got.Status != wantStatus || got.Message != wantMessage {
					t.Fatalf("status = %q %q, want %q %q", got.Status, got.Message, wantStatus, wantMessage)
				}
				if got.ExternalStatus != "completed/failedPending" || got.ExternalID != "42" {
					t.Fatalf("status = %+v, want completed/failedPending for 42", got)
				}
				if got.Progress != nil {
					t.Fatalf("progress = %+v, want none for a failed download", got.Progress)
				}
				if n := stub.configReads.Load(); n != 1 {
					t.Fatalf("download client settings read %d times, want 1", n)
				}
			})
		}
	}
}

// Only a failed download costs the extra call for the download client
// settings; a check of a download in progress makes none.
func TestCheckStatusReadsDownloadClientSettingsOnlyForFailedDownloads(t *testing.T) {
	for _, kind := range checkKinds {
		t.Run(kind.kind, func(t *testing.T) {
			stub := &failedDownloadStub{
				queue:  `[{"id":9,"movieId":42,"seriesId":42,"status":"downloading","trackedDownloadState":"downloading","downloadId":"abc","size":2000.0,"sizeleft":500.0}]`,
				config: `{"autoRedownloadFailed":false}`,
			}
			server := stub.serve(t)
			defer server.Close()

			got, err := kind.check(context.Background(), server)
			if err != nil {
				t.Fatalf("check returned error: %v", err)
			}
			if got.Status != StatusDownloading || got.Progress == nil {
				t.Fatalf("status = %+v, want downloading with progress", got)
			}
			if n := stub.configReads.Load(); n != 0 {
				t.Fatalf("download client settings read %d times, want none", n)
			}
		})
	}
}
