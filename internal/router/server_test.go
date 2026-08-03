package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Community/silo-plugins-requests-arr/internal/arr"
	"google.golang.org/protobuf/types/known/structpb"
)

func sibConn(id string, cfg map[string]any) *pluginv1.RouterConnection {
	s, _ := structpb.NewStruct(cfg)
	return &pluginv1.RouterConnection{Id: id, Config: s}
}

func TestValidateCharacterizesNormalDefaultRules(t *testing.T) {
	cases := []struct {
		name       string
		connection *pluginv1.RouterConnection
		siblings   []*pluginv1.RouterConnection
		want       map[string]string
	}{
		{
			name:       "rejects HD default on 4K instance",
			connection: sibConn("c1", map[string]any{"service_kind": "radarr", "is_default": true, "is_4k": true}),
			want:       map[string]string{"is_default": "the HD default cannot be a 4K server"},
		},
		{
			name:       "rejects 4K default on HD instance",
			connection: sibConn("c1", map[string]any{"service_kind": "radarr", "is_default_4k": true}),
			want:       map[string]string{"is_default_4k": "the 4K default must be a 4K server"},
		},
		{
			name:       "rejects second normal HD default of same kind",
			connection: sibConn("c2", map[string]any{"service_kind": "radarr", "is_default": true}),
			siblings:   []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "radarr", "is_default": true})},
			want:       map[string]string{"is_default": "radarr already has an HD default; unset it on the other connection first"},
		},
		{
			name:       "rejects second normal 4K default of same kind",
			connection: sibConn("c2", map[string]any{"service_kind": "radarr", "is_4k": true, "is_default_4k": true}),
			siblings:   []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "radarr", "is_4k": true, "is_default_4k": true})},
			want:       map[string]string{"is_default_4k": "radarr already has a 4K default; unset it on the other connection first"},
		},
		{
			name:       "allows normal and anime HD roles on one HD instance",
			connection: sibConn("c1", map[string]any{"service_kind": "radarr", "is_default": true, "is_anime_default": true}),
			want:       map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := New().Validate(context.Background(), &pluginv1.ValidateRequest{
				CapabilityId: "arr",
				Connection:   tc.connection,
				Siblings:     tc.siblings,
			})
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got := resp.GetFieldErrors(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("FieldErrors: want %#v got %#v", tc.want, got)
			}
		})
	}
}

func TestValidateAnimeDefaultRules(t *testing.T) {
	cases := []struct {
		name       string
		connection *pluginv1.RouterConnection
		siblings   []*pluginv1.RouterConnection
		want       map[string]string
	}{
		{"rejects anime HD default on 4K instance", sibConn("c1", map[string]any{"service_kind": "radarr", "is_4k": true, "is_anime_default": true}), nil, map[string]string{"is_anime_default": "the anime HD default cannot be a 4K server"}},
		{"rejects anime 4K default on HD instance", sibConn("c1", map[string]any{"service_kind": "radarr", "is_anime_default_4k": true}), nil, map[string]string{"is_anime_default_4k": "the anime 4K default must be a 4K server"}},
		{"rejects second anime HD default of same kind", sibConn("c2", map[string]any{"service_kind": "radarr", "is_anime_default": true}), []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "radarr", "is_anime_default": true})}, map[string]string{"is_anime_default": "radarr already has an anime HD default; unset it on the other connection first"}},
		{"rejects second anime 4K default of same kind", sibConn("c2", map[string]any{"service_kind": "sonarr", "is_4k": true, "is_anime_default_4k": true}), []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "sonarr", "is_4k": true, "is_anime_default_4k": true})}, map[string]string{"is_anime_default_4k": "sonarr already has an anime 4K default; unset it on the other connection first"}},
		{"allows anime role across kinds", sibConn("c2", map[string]any{"service_kind": "radarr", "is_anime_default": true}), []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "sonarr", "is_anime_default": true})}, map[string]string{}},
		{"allows valid anime HD assignment", sibConn("c1", map[string]any{"service_kind": "radarr", "is_anime_default": true}), nil, map[string]string{}},
		{"allows normal and anime 4K roles on one 4K instance", sibConn("c1", map[string]any{"service_kind": "radarr", "is_4k": true, "is_default_4k": true, "is_anime_default_4k": true}), nil, map[string]string{}},
		{"ignores config-less sibling", sibConn("c2", map[string]any{"service_kind": "radarr", "is_anime_default": true}), []*pluginv1.RouterConnection{{Id: "c1"}}, map[string]string{}},
		{"ignores current connection in siblings", sibConn("c1", map[string]any{"service_kind": "radarr", "is_anime_default": true}), []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": "radarr", "is_anime_default": true})}, map[string]string{}},
		{"ignores malformed sibling fields", sibConn("c2", map[string]any{"service_kind": "radarr", "is_anime_default": true}), []*pluginv1.RouterConnection{sibConn("c1", map[string]any{"service_kind": false, "is_anime_default": "true"})}, map[string]string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := New().Validate(context.Background(), &pluginv1.ValidateRequest{CapabilityId: "arr", Connection: tc.connection, Siblings: tc.siblings})
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got := resp.GetFieldErrors(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("FieldErrors: want %#v got %#v", tc.want, got)
			}
		})
	}
}

func TestValidateDoesNotRetainAnimeErrorsAcrossRequests(t *testing.T) {
	server := New()
	invalid, err := server.Validate(context.Background(), &pluginv1.ValidateRequest{Connection: sibConn("c1", map[string]any{"service_kind": "radarr", "is_4k": true, "is_anime_default": true})})
	if err != nil || !reflect.DeepEqual(invalid.GetFieldErrors(), map[string]string{"is_anime_default": "the anime HD default cannot be a 4K server"}) {
		t.Fatalf("invalid Validate: response=%#v err=%v", invalid, err)
	}
	valid, err := server.Validate(context.Background(), &pluginv1.ValidateRequest{Connection: sibConn("c1", map[string]any{"service_kind": "radarr", "is_anime_default": true})})
	if err != nil || len(valid.GetFieldErrors()) != 0 {
		t.Fatalf("valid Validate: response=%#v err=%v", valid, err)
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

type fulfillProbe struct {
	mu          sync.Mutex
	lookupCalls int
	addCalls    int
	body        map[string]any
	server      *httptest.Server
}

func newFulfillProbe(t *testing.T, kind string) *fulfillProbe {
	t.Helper()
	p := &fulfillProbe{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookupPath, addPath := "/api/v3/movie/lookup/tmdb", "/api/v3/movie"
		lookupResponse, addResponse := `{"title":"Movie","tmdbId":42,"titleSlug":"movie"}`, `{"id":777,"tmdbId":42}`
		if kind == "sonarr" {
			lookupPath, addPath = "/api/v3/series/lookup", "/api/v3/series"
			lookupResponse, addResponse = `[{"title":"Series","tvdbId":7,"titleSlug":"series"}]`, `{"id":888,"tvdbId":7}`
		}
		switch r.URL.Path {
		case lookupPath:
			if r.Method != http.MethodGet {
				t.Errorf("%s lookup: want GET got %s", kind, r.Method)
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				return
			}
			p.mu.Lock()
			p.lookupCalls++
			p.mu.Unlock()
			_, _ = w.Write([]byte(lookupResponse))
		case addPath:
			if r.Method != http.MethodPost {
				t.Errorf("%s add: want POST got %s", kind, r.Method)
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				return
			}
			body := map[string]any{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode %s add body: %v", kind, err)
				http.Error(w, "invalid add body", http.StatusBadRequest)
				return
			}
			p.mu.Lock()
			p.addCalls++
			p.body = body
			p.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(addResponse))
		default:
			t.Errorf("unexpected %s request: %s %s", kind, r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	return p
}

func (p *fulfillProbe) connection(id string, config map[string]any) *pluginv1.RouterConnection {
	conn := sibConn(id, config)
	conn.BaseUrl = p.server.URL
	conn.ApiKey = "test-key"
	return conn
}

func (p *fulfillProbe) snapshot() (int, int, map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lookupCalls, p.addCalls, p.body
}

func TestFulfillRoutesAnimeToDedicatedARRInstance(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		mediaType  string
		ids        map[string]string
		wantFields map[string]any
	}{
		{"Radarr movie", "radarr", "movie", map[string]string{"tmdb": "42"}, map[string]any{"rootFolderPath": "/dedicated-anime", "qualityProfileId": float64(21), "tags": []any{float64(21), float64(22)}}},
		{"Sonarr series", "sonarr", "series", map[string]string{"tvdb": "7"}, map[string]any{"seriesType": "anime"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			normal, anime := newFulfillProbe(t, tc.kind), newFulfillProbe(t, tc.kind)
			defer normal.server.Close()
			defer anime.server.Close()
			normalConfig := map[string]any{"service_kind": tc.kind, "is_default": true, "root_folder": "/normal", "quality_profile_id": 1, "tags": []any{1}}
			animeConfig := map[string]any{"service_kind": tc.kind, "is_anime_default": true, "root_folder": "/dedicated-standard", "quality_profile_id": 20, "tags": []any{20}}
			if tc.kind == "radarr" {
				animeConfig["anime_root_folder"] = "/dedicated-anime"
				animeConfig["anime_quality_profile_id"] = 21
				animeConfig["anime_tags"] = []any{21, 22}
			}

			// When
			resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
				Request:     &pluginv1.RequestDescriptor{MediaType: tc.mediaType, IsAnime: true, ExternalIds: tc.ids},
				Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
				Connections: []*pluginv1.RouterConnection{normal.connection("normal", normalConfig), anime.connection("anime", animeConfig)},
			})

			// Then
			if err != nil || len(resp.GetTargets()) != 1 || resp.GetTargets()[0].GetConnectionId() != "anime" {
				t.Fatalf("Fulfill: response=%#v err=%v", resp, err)
			}
			if lookup, add, _ := normal.snapshot(); lookup != 0 || add != 0 {
				t.Fatalf("normal server calls: want 0/0 got %d/%d", lookup, add)
			}
			lookup, add, body := anime.snapshot()
			if lookup != 1 || add != 1 {
				t.Fatalf("anime server calls: want 1/1 got %d/%d", lookup, add)
			}
			for field, want := range tc.wantFields {
				if !reflect.DeepEqual(body[field], want) {
					t.Fatalf("%s payload %s: want %#v got %#v", tc.kind, field, want, body[field])
				}
			}
		})
	}
}

func TestFulfillAnimeFallsBackToNormalRole(t *testing.T) {
	cases := []struct {
		name        string
		config      map[string]any
		wantRoot    string
		wantProfile float64
		wantTags    []any
	}{
		{"anime enabled applies overlays", map[string]any{"anime_enabled": true, "anime_root_folder": "/anime", "anime_quality_profile_id": 2, "anime_tags": []any{2}}, "/anime", 2, []any{float64(2)}},
		{"anime disabled remains standard", map[string]any{"anime_enabled": false, "anime_root_folder": "/anime", "anime_quality_profile_id": 2, "anime_tags": []any{2}}, "/standard", 1, []any{float64(1)}},
		{"wrong anime tier remains standard", map[string]any{"is_anime_default_4k": true}, "/standard", 1, []any{float64(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			server := newFulfillProbe(t, "radarr")
			defer server.server.Close()
			config := map[string]any{"service_kind": "radarr", "is_default": true, "root_folder": "/standard", "quality_profile_id": 1, "tags": []any{1}}
			for field, value := range tc.config {
				config[field] = value
			}

			// When
			resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
				Request:     &pluginv1.RequestDescriptor{MediaType: "movie", IsAnime: true, ExternalIds: map[string]string{"tmdb": "42"}},
				Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
				Connections: []*pluginv1.RouterConnection{server.connection("normal", config)},
			})

			// Then
			if err != nil || len(resp.GetTargets()) != 1 || resp.GetTargets()[0].GetConnectionId() != "normal" {
				t.Fatalf("Fulfill: response=%#v err=%v", resp, err)
			}
			lookup, add, body := server.snapshot()
			if lookup != 1 || add != 1 {
				t.Fatalf("server calls: want 1/1 got %d/%d", lookup, add)
			}
			if body["rootFolderPath"] != tc.wantRoot || body["qualityProfileId"] != tc.wantProfile || !reflect.DeepEqual(body["tags"], tc.wantTags) {
				t.Fatalf("Radarr payload: want %q/%v/%#v got %#v", tc.wantRoot, tc.wantProfile, tc.wantTags, body)
			}
		})
	}
}

func TestFulfillAnimeReturnsExistingZeroTargetMessageWithoutMatchingRole(t *testing.T) {
	// Given
	server := newFulfillProbe(t, "radarr")
	defer server.server.Close()

	// When
	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", IsAnime: true, ExternalIds: map[string]string{"tmdb": "42"}},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
		Connections: []*pluginv1.RouterConnection{server.connection("wrong-kind", map[string]any{"service_kind": "sonarr", "is_anime_default": true, "root_folder": "/tv", "quality_profile_id": 1})},
	})

	// Then
	if err != nil || len(resp.GetTargets()) != 0 || resp.GetMessage() != "no radarr instance configured for the requested quality" {
		t.Fatalf("Fulfill: response=%#v err=%v", resp, err)
	}
	if lookup, add, _ := server.snapshot(); lookup != 0 || add != 0 {
		t.Fatalf("wrong-kind server calls: want 0/0 got %d/%d", lookup, add)
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
