package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// CheckStatus carries each target's download progress into the response, and
// leaves it unset for a target with nothing in the queue.
func TestCheckStatusCarriesDownloadProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/queue/details" && r.URL.Query().Get("movieId") == "42":
			_, _ = w.Write([]byte(`[{"id":9,"movieId":42,"status":"downloading","trackedDownloadStatus":"ok","trackedDownloadState":"downloading",
				"downloadId":"abc","size":2000.0,"sizeleft":1500.0,"estimatedCompletionTime":"2026-09-28T12:10:00Z"}]`))
		case r.URL.Path == "/api/v3/queue/details" && r.URL.Query().Get("movieId") == "43":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/movie/43":
			_, _ = w.Write([]byte(`{"id":43,"hasFile":true}`))
		default:
			http.Error(w, "unexpected "+r.URL.String(), http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg, err := structpb.NewStruct(map[string]any{"service_kind": "radarr"})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		CapabilityId: "arr",
		Request:      &pluginv1.RequestDescriptor{MediaType: "movie", ExternalIds: map[string]string{"tmdb": "550"}},
		Targets: []*pluginv1.TargetRef{
			{Quality: "1080p", ConnectionId: "hd", ExternalId: "42"},
			{Quality: "2160p", ConnectionId: "uhd", ExternalId: "43"},
		},
		Connections: []*pluginv1.RouterConnection{
			{Id: "hd", BaseUrl: srv.URL, ApiKey: "k", Config: cfg},
			{Id: "uhd", BaseUrl: srv.URL, ApiKey: "k", Config: cfg},
		},
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	statuses := resp.GetStatuses()
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v, want 2", statuses)
	}

	downloading := statuses[0]
	if downloading.GetStatus() != "downloading" {
		t.Fatalf("1080p status = %q, want downloading", downloading.GetStatus())
	}
	progress := downloading.GetProgress()
	if progress.GetPhase() != "downloading" || progress.GetBytesTotal() != 2000 || progress.GetBytesLeft() != 1500 || progress.GetDownloads() != 1 {
		t.Fatalf("1080p progress = %+v, want downloading 1500/2000 left over 1 download", progress)
	}
	want := time.Date(2026, 9, 28, 12, 10, 0, 0, time.UTC)
	if eta := progress.GetEstimatedCompletion(); eta == nil || !eta.AsTime().Equal(want) {
		t.Fatalf("1080p estimated completion = %v, want %v", eta, want)
	}

	completed := statuses[1]
	if completed.GetStatus() != "completed" || completed.GetProgress() != nil {
		t.Fatalf("2160p = status %q progress %+v, want completed without progress", completed.GetStatus(), completed.GetProgress())
	}
}
