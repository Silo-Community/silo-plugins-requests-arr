package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Community/silo-plugins-requests-arr/internal/arr"
	"google.golang.org/protobuf/types/known/structpb"
)

func sibConn(id string, cfg map[string]any) *pluginv1.RouterConnection {
	s, _ := structpb.NewStruct(cfg)
	return &pluginv1.RouterConnection{Id: id, Config: s}
}

func TestValidateRejectsSecondHDDefaultSameKind(t *testing.T) {
	resp, err := (&Server{}).Validate(context.Background(), &pluginv1.ValidateRequest{
		CapabilityId: "arr",
		Connection:   sibConn("c2", map[string]any{"service_kind": "radarr", "is_default": true}),
		Siblings:     []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "radarr", "is_default": true})},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.GetFieldErrors()["is_default"] == "" {
		t.Fatalf("expected is_default conflict error, got %+v", resp.GetFieldErrors())
	}
}

func TestValidateAllowsHDDefaultDifferentKind(t *testing.T) {
	resp, _ := (&Server{}).Validate(context.Background(), &pluginv1.ValidateRequest{
		CapabilityId: "arr",
		Connection:   sibConn("c2", map[string]any{"service_kind": "radarr", "is_default": true}),
		Siblings:     []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "sonarr", "is_default": true})},
	})
	if resp.GetFieldErrors()["is_default"] != "" {
		t.Fatalf("different kind must not conflict, got %+v", resp.GetFieldErrors())
	}
}

func TestValidateRejectsSecond4KDefaultSameKind(t *testing.T) {
	resp, _ := (&Server{}).Validate(context.Background(), &pluginv1.ValidateRequest{
		CapabilityId: "arr",
		Connection:   sibConn("c2", map[string]any{"service_kind": "radarr", "is_4k": true, "is_default_4k": true}),
		Siblings:     []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "radarr", "is_4k": true, "is_default_4k": true})},
	})
	if resp.GetFieldErrors()["is_default_4k"] == "" {
		t.Fatalf("expected is_default_4k conflict, got %+v", resp.GetFieldErrors())
	}
}

func TestValidateToleratesSiblingWithoutConfig(t *testing.T) {
	resp, err := (&Server{}).Validate(context.Background(), &pluginv1.ValidateRequest{
		CapabilityId: "arr",
		Connection:   sibConn("c2", map[string]any{"service_kind": "radarr", "is_default": true}),
		Siblings:     []*pluginv1.RouterConnection{{Id: "c1"}},
	})
	if err != nil {
		t.Fatalf("Validate must not error on a config-less sibling: %v", err)
	}
	if resp.GetFieldErrors()["is_default"] != "" {
		t.Fatalf("a config-less sibling must not conflict, got %+v", resp.GetFieldErrors())
	}
}

func TestFulfillSubmitsMovieToDefaultInstance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"X","tmdbId":42,"titleSlug":"x"}`))
		case "/api/v3/movie":
			if r.Method != http.MethodPost {
				w.Write([]byte(`[]`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":777,"tmdbId":42}`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg, err := structpb.NewStruct(map[string]any{
		"service_kind":       "radarr",
		"is_default":         true,
		"root_folder":        "/movies",
		"quality_profile_id": float64(1),
	})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		CapabilityId: "arr",
		Request: &pluginv1.RequestDescriptor{
			MediaType:   "movie",
			ExternalIds: map[string]string{"tmdb": "42"},
			Title:       "X",
		},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p", Is4K: false}},
		Connections: []*pluginv1.RouterConnection{{Id: "c1", BaseUrl: srv.URL, ApiKey: "k", Config: cfg}},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 1 {
		t.Fatalf("want 1 target, got %d msg=%q", len(resp.GetTargets()), resp.GetMessage())
	}
	tgt := resp.GetTargets()[0]
	if tgt.GetStatus() != "queued" {
		t.Fatalf("status: want queued got %q msg=%q", tgt.GetStatus(), tgt.GetMessage())
	}
	if tgt.GetExternalId() != "777" {
		t.Fatalf("external id: want 777 got %q", tgt.GetExternalId())
	}
	if tgt.GetConnectionId() != "c1" {
		t.Fatalf("connection id: want c1 got %q", tgt.GetConnectionId())
	}
	if tgt.GetQuality() != "1080p" {
		t.Fatalf("quality: want 1080p got %q", tgt.GetQuality())
	}
}

func TestFulfillNoMatchingInstance(t *testing.T) {
	cfg, _ := structpb.NewStruct(map[string]any{
		"service_kind": "radarr",
		"is_default":   true,
	})
	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", ExternalIds: map[string]string{"tmdb": "1"}},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "2160p", Is4K: true}}, // no 4k default configured
		Connections: []*pluginv1.RouterConnection{{Id: "c1", BaseUrl: "http://unused", Config: cfg}},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 0 {
		t.Fatalf("want 0 targets, got %d", len(resp.GetTargets()))
	}
	if resp.GetMessage() == "" {
		t.Fatalf("want explanatory message, got empty")
	}
}

func TestValidateRejectsHDDefaultThatIsAlso4K(t *testing.T) {
	cfg, _ := structpb.NewStruct(map[string]any{"service_kind": "radarr", "is_default": true, "is_4k": true})
	resp, err := New().Validate(context.Background(), &pluginv1.ValidateRequest{
		CapabilityId: "arr", Connection: &pluginv1.RouterConnection{Id: "c1", Config: cfg},
	})
	if err != nil {
		t.Fatalf("Validate err: %v", err)
	}
	if resp.GetFieldErrors()["is_default"] == "" && resp.GetFormError() == "" {
		t.Fatal("expected a validation error for HD default that is also 4K")
	}
}

func TestValidateRejects4KDefaultOnNon4K(t *testing.T) {
	cfg, _ := structpb.NewStruct(map[string]any{"service_kind": "radarr", "is_default_4k": true, "is_4k": false})
	resp, _ := New().Validate(context.Background(), &pluginv1.ValidateRequest{
		CapabilityId: "arr", Connection: &pluginv1.RouterConnection{Id: "c1", Config: cfg},
	})
	if resp.GetFieldErrors()["is_default_4k"] == "" && resp.GetFormError() == "" {
		t.Fatal("expected a validation error for 4K default on a non-4K server")
	}
}

func TestValidateAcceptsConsistentConfig(t *testing.T) {
	cfg, _ := structpb.NewStruct(map[string]any{"service_kind": "radarr", "is_default": true, "is_4k": false})
	resp, _ := New().Validate(context.Background(), &pluginv1.ValidateRequest{
		CapabilityId: "arr", Connection: &pluginv1.RouterConnection{Id: "c1", Config: cfg},
	})
	if len(resp.GetFieldErrors()) != 0 || resp.GetFormError() != "" {
		t.Fatalf("expected no errors, got fe=%v form=%q", resp.GetFieldErrors(), resp.GetFormError())
	}
}

func TestRootFolderLabelEncodesFreeSpaceAndAccessibility(t *testing.T) {
	cases := []struct {
		name string
		rf   arr.IntegrationRootFolder
		want string
	}{
		{"inaccessible", arr.IntegrationRootFolder{Path: "/movies", Accessible: false}, "/movies (inaccessible)"},
		{"free space gib", arr.IntegrationRootFolder{Path: "/movies", Accessible: true, FreeSpace: 1610612736}, "/movies (1.5 GiB free)"},
		{"free space tib", arr.IntegrationRootFolder{Path: "/tv", Accessible: true, FreeSpace: 1649267441664}, "/tv (1.5 TiB free)"},
		{"no free space reported", arr.IntegrationRootFolder{Path: "/data", Accessible: true, FreeSpace: 0}, "/data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootFolderLabel(tc.rf); got != tc.want {
				t.Fatalf("rootFolderLabel: want %q got %q", tc.want, got)
			}
		})
	}
}

func TestListConfigOptionsRootFolderLabelsCarryFreeSpace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			w.Write([]byte(`{"appName":"Radarr","version":"5.2.0"}`))
		case "/api/v3/rootfolder":
			w.Write([]byte(`[{"path":"/movies","freeSpace":1610612736,"totalSpace":2000000000,"accessible":true},{"path":"/old","accessible":false}]`))
		case "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":1,"name":"HD"}]`))
		case "/api/v3/tag":
			w.Write([]byte(`[{"id":3,"label":"kids"}]`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg, err := structpb.NewStruct(map[string]any{"service_kind": "radarr"})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	resp, err := New().ListConfigOptions(context.Background(), &pluginv1.ListConfigOptionsRequest{
		Connection: &pluginv1.RouterConnection{Id: "c1", BaseUrl: srv.URL, ApiKey: "k", Config: cfg},
	})
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}

	for _, field := range []string{"root_folder", "anime_root_folder"} {
		list := resp.GetOptionsByField()[field]
		if list == nil || len(list.GetOptions()) != 2 {
			t.Fatalf("%s: want 2 options, got %v", field, list)
		}
		accessible := list.GetOptions()[0]
		if accessible.GetValue() != "/movies" {
			t.Fatalf("%s: value must stay bare path, got %q", field, accessible.GetValue())
		}
		if accessible.GetLabel() != "/movies (1.5 GiB free)" {
			t.Fatalf("%s: label want free-space hint, got %q", field, accessible.GetLabel())
		}
		inaccessible := list.GetOptions()[1]
		if inaccessible.GetValue() != "/old" {
			t.Fatalf("%s: value must stay bare path, got %q", field, inaccessible.GetValue())
		}
		if inaccessible.GetLabel() != "/old (inaccessible)" {
			t.Fatalf("%s: label want inaccessible hint, got %q", field, inaccessible.GetLabel())
		}
	}
}
