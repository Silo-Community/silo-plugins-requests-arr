package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// Fulfill hands the descriptor's seasons to Sonarr, normalized, and they take
// precedence over the connection's monitor policy.
func TestFulfillSeriesSendsRequestedSeasons(t *testing.T) {
	var posted struct {
		Seasons []struct {
			SeasonNumber int  `json:"seasonNumber"`
			Monitored    bool `json:"monitored"`
		} `json:"seasons"`
		AddOptions map[string]any `json:"addOptions"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/series/lookup":
			_, _ = w.Write([]byte(`[{"title":"Doctor Who","tvdbId":76107,"seasons":[{"seasonNumber":0},{"seasonNumber":1},{"seasonNumber":2},{"seasonNumber":3}]}]`))
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

	cfg, err := structpb.NewStruct(map[string]any{
		"service_kind": "sonarr", "is_default": true, "root_folder": "/tv",
		"quality_profile_id": float64(1), "monitor": "all",
	})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		CapabilityId: "arr",
		Request: &pluginv1.RequestDescriptor{
			MediaType:   "series",
			ExternalIds: map[string]string{"tvdb": "76107"},
			Title:       "Doctor Who",
			Seasons:     []int32{3, 1, 3, -1},
		},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
		Connections: []*pluginv1.RouterConnection{{Id: "c1", BaseUrl: srv.URL, ApiKey: "k", Config: cfg}},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 1 || resp.GetTargets()[0].GetStatus() != "queued" {
		t.Fatalf("targets = %+v msg=%q, want one queued target", resp.GetTargets(), resp.GetMessage())
	}
	var monitored []int
	for _, s := range posted.Seasons {
		if s.Monitored {
			monitored = append(monitored, s.SeasonNumber)
		}
	}
	if !slices.Equal(monitored, []int{1, 3}) {
		t.Fatalf("monitored seasons = %v, want [1 3]", monitored)
	}
	if _, ok := posted.AddOptions["monitor"]; ok {
		t.Fatalf("addOptions.monitor = %v, want unset for a season request", posted.AddOptions["monitor"])
	}
}

func TestDescriptorSeasonsOnlyApplyToSeries(t *testing.T) {
	movie := descriptorToRequest(&pluginv1.RequestDescriptor{MediaType: "movie", Seasons: []int32{1}})
	if movie.Seasons != nil {
		t.Fatalf("movie seasons = %v, want none", movie.Seasons)
	}
	whole := descriptorToRequest(&pluginv1.RequestDescriptor{MediaType: "series"})
	if whole.Seasons != nil {
		t.Fatalf("whole-series seasons = %v, want none", whole.Seasons)
	}
	specials := descriptorToRequest(&pluginv1.RequestDescriptor{MediaType: "series", Seasons: []int32{2, 0, 2}})
	if !slices.Equal(specials.Seasons, []int{0, 2}) {
		t.Fatalf("seasons = %v, want [0 2]", specials.Seasons)
	}
}
