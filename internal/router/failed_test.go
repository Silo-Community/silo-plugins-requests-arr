package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// A failed download that Radarr will not replace fails its target with the
// failure as the message, so Silo marks the request failed and an admin can
// retry it. One Radarr replaces, or whose settings cannot be read, keeps its
// target queued, and neither drops the other targets from the response.
func TestCheckStatusReportsFailedDownloads(t *testing.T) {
	radarr := func(config string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v3/queue/details":
				_, _ = w.Write([]byte(`[{"id":9,"movieId":42,"title":"Movie.2026.1080p","status":"completed","trackedDownloadStatus":"error",
					"trackedDownloadState":"failedPending","downloadId":"abc","size":2000.0,"sizeleft":0.0}]`))
			case "/api/v3/config/downloadclient":
				if config == "" {
					http.Error(w, "database is locked", http.StatusInternalServerError)
					return
				}
				_, _ = w.Write([]byte(config))
			default:
				http.Error(w, "unexpected "+r.URL.String(), http.StatusNotFound)
			}
		}))
	}
	noRetry := radarr(`{"autoRedownloadFailed":false}`)
	defer noRetry.Close()
	retry := radarr(`{"autoRedownloadFailed":true}`)
	defer retry.Close()
	unreadable := radarr("")
	defer unreadable.Close()

	cfg, err := structpb.NewStruct(map[string]any{"service_kind": "radarr"})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		CapabilityId: "arr",
		Request:      &pluginv1.RequestDescriptor{MediaType: "movie", ExternalIds: map[string]string{"tmdb": "550"}},
		Targets: []*pluginv1.TargetRef{
			{Quality: "1080p", ConnectionId: "no-retry", ExternalId: "42"},
			{Quality: "2160p", ConnectionId: "retry", ExternalId: "42"},
			{Quality: "720p", ConnectionId: "unreadable", ExternalId: "42"},
		},
		Connections: []*pluginv1.RouterConnection{
			{Id: "no-retry", BaseUrl: noRetry.URL, ApiKey: "k", Config: cfg},
			{Id: "retry", BaseUrl: retry.URL, ApiKey: "k", Config: cfg},
			{Id: "unreadable", BaseUrl: unreadable.URL, ApiKey: "k", Config: cfg},
		},
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	statuses := resp.GetStatuses()
	if len(statuses) != 3 {
		t.Fatalf("statuses = %+v, want 3", statuses)
	}

	const failure = "external queue failed for Movie.2026.1080p: completed/failedPending"
	failed := statuses[0]
	if failed.GetStatus() != "failed" || failed.GetMessage() != failure {
		t.Fatalf("1080p = status %q message %q, want failed with %q", failed.GetStatus(), failed.GetMessage(), failure)
	}
	if failed.GetExternalStatus() != "completed/failedPending" || failed.GetProgress() != nil {
		t.Fatalf("1080p = external status %q progress %+v, want completed/failedPending without progress",
			failed.GetExternalStatus(), failed.GetProgress())
	}
	for _, st := range statuses[1:] {
		want := failure + "; Radarr is looking for another release"
		if st.GetStatus() != "queued" || st.GetMessage() != want || st.GetProgress() != nil {
			t.Fatalf("%s = status %q message %q progress %+v, want queued with %q and no progress",
				st.GetQuality(), st.GetStatus(), st.GetMessage(), st.GetProgress(), want)
		}
	}
}

// A title deleted from Sonarr or Radarr after Silo sent it 404s on every
// check from then on, so its target fails with a message that says so and
// the request can be retried or closed. Any other error stays skipped as
// transient and leaves the target as it was.
func TestCheckStatusFailsTargetsDeletedInArr(t *testing.T) {
	arrServer := func(itemPath string, itemStatus int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v3/queue/details":
				_, _ = w.Write([]byte(`[]`))
			case itemPath:
				http.Error(w, `{"message":"NotFound"}`, itemStatus)
			default:
				http.Error(w, "unexpected "+r.URL.String(), http.StatusTeapot)
			}
		}))
	}
	cases := []struct {
		name, mediaType, itemPath string
		itemStatus                int
		wantFailed                bool
		wantMessage               string
	}{
		{"series deleted in Sonarr", "series", "/api/v3/series/536", http.StatusNotFound, true, "series no longer present in Sonarr"},
		{"movie deleted in Radarr", "movie", "/api/v3/movie/536", http.StatusNotFound, true, "movie no longer present in Radarr"},
		{"Sonarr erroring", "series", "/api/v3/series/536", http.StatusInternalServerError, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := arrServer(tc.itemPath, tc.itemStatus)
			defer srv.Close()
			resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
				CapabilityId: "arr",
				Request:      &pluginv1.RequestDescriptor{MediaType: tc.mediaType, ExternalIds: map[string]string{"tmdb": "550", "tvdb": "81189"}},
				Targets:      []*pluginv1.TargetRef{{Quality: "1080p", ConnectionId: "arr", ExternalId: "536"}},
				Connections:  []*pluginv1.RouterConnection{{Id: "arr", BaseUrl: srv.URL, ApiKey: "k"}},
			})
			if err != nil {
				t.Fatalf("CheckStatus: %v", err)
			}
			statuses := resp.GetStatuses()
			if !tc.wantFailed {
				if len(statuses) != 0 {
					t.Fatalf("statuses = %+v, want none", statuses)
				}
				return
			}
			if len(statuses) != 1 {
				t.Fatalf("statuses = %+v, want 1", statuses)
			}
			st := statuses[0]
			if st.GetStatus() != "failed" || st.GetMessage() != tc.wantMessage || st.GetQuality() != "1080p" || st.GetConnectionId() != "arr" {
				t.Fatalf("status = %+v, want failed 1080p on arr with %q", st, tc.wantMessage)
			}
		})
	}
}
