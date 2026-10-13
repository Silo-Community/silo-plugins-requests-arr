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

// sonarrV5 answers the requests a Sonarr v5 connection's options and a series
// add make, and records the added series' language.
func sonarrV5(t *testing.T, postedLanguage *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/system/status":
			_, _ = w.Write([]byte(`{"appName":"Sonarr","version":"5.0.0.2569"}`))
		case r.URL.Path == "/api/v5/settings/metadata":
			_, _ = w.Write([]byte(`{"preferredMetadataLanguage":4}`))
		case r.URL.Path == "/api/v3/language":
			_, _ = w.Write([]byte(`[{"id":0,"name":"Unknown"},{"id":1,"name":"English"},{"id":4,"name":"German"}]`))
		case r.URL.Path == "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"path":"/tv","accessible":true}]`))
		case r.URL.Path == "/api/v3/qualityprofile", r.URL.Path == "/api/v3/tag":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/series/lookup":
			_, _ = w.Write([]byte(`[{"title":"Doctor Who","tvdbId":76107}]`))
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodPost:
			var body struct {
				Language map[string]any `json:"language"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode posted series: %v", err)
			}
			*postedLanguage = body.Language
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":9,"tvdbId":76107}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListConfigOptionsListsSonarrMetadataLanguages(t *testing.T) {
	srv := sonarrV5(t, new(map[string]any))
	cfg, err := structpb.NewStruct(map[string]any{"service_kind": "sonarr"})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	resp, err := New().ListConfigOptions(context.Background(), &pluginv1.ListConfigOptionsRequest{
		Connection: &pluginv1.RouterConnection{Id: "c1", BaseUrl: srv.URL, ApiKey: "k", Config: cfg},
	})
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	var got []string
	for _, o := range resp.GetOptionsByField()["metadata_language"].GetOptions() {
		got = append(got, o.GetValue()+"="+o.GetLabel())
	}
	if len(got) != 2 || got[0] != "4=German" || got[1] != "1=English" {
		t.Fatalf("metadata_language options = %v, want [4=German 1=English]", got)
	}
}

// The connection's metadata_language reaches the series add, as it does when a
// route overrides it.
func TestFulfillSeriesSendsConnectionMetadataLanguage(t *testing.T) {
	var posted map[string]any
	srv := sonarrV5(t, &posted)
	cfg, err := structpb.NewStruct(map[string]any{
		"service_kind": "sonarr", "is_default": true, "root_folder": "/tv",
		"quality_profile_id": float64(1), "metadata_language": float64(1),
	})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	_, err = New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		CapabilityId: "arr",
		Request: &pluginv1.RequestDescriptor{
			MediaType:   "series",
			ExternalIds: map[string]string{"tvdb": "76107"},
			Title:       "Doctor Who",
		},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
		Connections: []*pluginv1.RouterConnection{{Id: "c1", BaseUrl: srv.URL, ApiKey: "k", Config: cfg}},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if posted["id"] != float64(1) {
		t.Fatalf("posted language = %v, want the connection's (id 1), not Sonarr's preferred", posted)
	}
}
