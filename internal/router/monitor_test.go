package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// fulfillSeriesAddOptions runs a series Fulfill against a fake Sonarr built from
// cfg and returns the addOptions the plugin posted.
func fulfillSeriesAddOptions(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	var posted struct {
		AddOptions map[string]any `json:"addOptions"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/series/lookup":
			_, _ = w.Write([]byte(`[{"title":"Doctor Who","tvdbId":76107,"titleSlug":"doctor-who"}]`))
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Errorf("decode posted series: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":9,"tvdbId":76107}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	base := map[string]any{
		"service_kind":       "sonarr",
		"is_default":         true,
		"root_folder":        "/tv",
		"quality_profile_id": float64(1),
		"search_on_add":      true,
	}
	for k, v := range cfg {
		base[k] = v
	}
	s, err := structpb.NewStruct(base)
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		CapabilityId: "arr",
		Request: &pluginv1.RequestDescriptor{
			MediaType:   "series",
			ExternalIds: map[string]string{"tvdb": "76107"},
			Title:       "Doctor Who",
		},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
		Connections: []*pluginv1.RouterConnection{{Id: "c1", BaseUrl: srv.URL, ApiKey: "k", Config: s}},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 1 || resp.GetTargets()[0].GetStatus() != "queued" {
		t.Fatalf("targets = %+v msg=%q, want one queued target", resp.GetTargets(), resp.GetMessage())
	}
	return posted.AddOptions
}

func TestFulfillSeriesSendsConfiguredMonitorPolicy(t *testing.T) {
	got := fulfillSeriesAddOptions(t, map[string]any{"monitor": "lastSeason"})
	if got["monitor"] != "lastSeason" {
		t.Fatalf("addOptions.monitor = %v, want lastSeason", got["monitor"])
	}
	if got["searchForMissingEpisodes"] != true {
		t.Fatalf("addOptions.searchForMissingEpisodes = %v, want true", got["searchForMissingEpisodes"])
	}
}

func TestFulfillSeriesDefaultsMonitorPolicyToAll(t *testing.T) {
	got := fulfillSeriesAddOptions(t, nil)
	if got["monitor"] != "all" {
		t.Fatalf("addOptions.monitor = %v, want all", got["monitor"])
	}
}

func TestValidateMonitorPolicy(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]any
		wantErr bool
	}{
		{"known sonarr policy", map[string]any{"service_kind": "sonarr", "monitor": "future"}, false},
		{"unset falls back to default", map[string]any{"service_kind": "sonarr"}, false},
		{"unknown sonarr policy", map[string]any{"service_kind": "sonarr", "monitor": "everything"}, true},
		{"radarr ignores monitor", map[string]any{"service_kind": "radarr", "monitor": "everything"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := New().Validate(context.Background(), &pluginv1.ValidateRequest{
				CapabilityId: "arr", Connection: sibConn("c1", tc.cfg),
			})
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got := resp.GetFieldErrors()["monitor"] != ""; got != tc.wantErr {
				t.Fatalf("monitor field error = %q, want error %v", resp.GetFieldErrors()["monitor"], tc.wantErr)
			}
		})
	}
}
