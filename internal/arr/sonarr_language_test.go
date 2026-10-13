package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sonarrV5Languages answers Sonarr v5's language list: the placeholder Any and
// Unknown entries come first, as Sonarr lists them.
const sonarrV5Languages = `[{"id":-1,"name":"Any"},{"id":0,"name":"Unknown"},{"id":1,"name":"English"},{"id":4,"name":"German"},{"id":8,"name":"Japanese"}]`

// fakeSonarrV5 is a Sonarr v5 that adds a new series. It records the raw add
// body and how often the metadata settings were read.
type fakeSonarrV5 struct {
	t        *testing.T
	settings string // GET /api/v5/settings/metadata body; "" answers 500
	posted   map[string]any
	reads    int
}

func (f *fakeSonarrV5) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v5/settings/metadata":
		f.reads++
		if f.settings == "" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(f.settings))
	case r.URL.Path == "/api/v3/language":
		_, _ = w.Write([]byte(sonarrV5Languages))
	case r.URL.Path == "/api/v3/rootfolder":
		_, _ = w.Write([]byte(`[{"path":"/tv","accessible":true}]`))
	case r.URL.Path == "/api/v3/qualityprofile":
		_, _ = w.Write([]byte(`[{"id":3,"name":"HD-1080p"}]`))
	case r.URL.Path == "/api/v3/tag":
		_, _ = w.Write([]byte(`[]`))
	case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`[]`))
	case r.URL.Path == "/api/v3/series/lookup":
		if got := r.URL.Query().Get("language"); got != "" {
			f.t.Errorf("lookup language = %q, want none: Sonarr rejects languages it has no ISO code for", got)
		}
		_, _ = w.Write([]byte(`[{"title":"Game of Thrones","tvdbId":121361,"titleSlug":"game-of-thrones"}]`))
	case r.URL.Path == "/api/v3/series" && r.Method == http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&f.posted); err != nil {
			f.t.Fatalf("decode posted series: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":24,"tvdbId":121361}`))
	default:
		f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
	}
}

func submitV5Series(t *testing.T, handler http.Handler, options map[string]any) error {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	qualityProfileID := 3
	tvdbID := 121361
	_, err := NewSonarrClient(server.Client()).SubmitSeries(context.Background(), Request{
		MediaType: MediaTypeSeries,
		TVDBID:    &tvdbID,
	}, Instance{
		Kind:             "sonarr",
		BaseURL:          server.URL,
		APIKeyRef:        "sonarr-key",
		RootFolder:       "/tv",
		QualityProfileID: &qualityProfileID,
		Options:          options,
	})
	return err
}

// Sonarr v5 adds a series through the v3 API in English unless told
// otherwise, so the plugin names the connection's language, or else Sonarr's
// preferred one.
func TestSubmitSeriesSendsMetadataLanguageOnSonarrV5(t *testing.T) {
	cases := []struct {
		name      string
		options   map[string]any
		wantID    float64
		wantReads int
	}{
		{"preferred language", nil, 4, 1},
		{"connection language", map[string]any{"metadata_language": float64(8)}, 8, 0},
		{"connection language as text", map[string]any{"metadata_language": "8"}, 8, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSonarrV5{t: t, settings: `{"preferredMetadataLanguage":4}`}
			if err := submitV5Series(t, f, tc.options); err != nil {
				t.Fatalf("SubmitSeries: %v", err)
			}
			language, _ := f.posted["language"].(map[string]any)
			if language["id"] != tc.wantID {
				t.Fatalf("posted language = %v, want id %v", f.posted["language"], tc.wantID)
			}
			if f.reads != tc.wantReads {
				t.Fatalf("metadata settings read %d times, want %d", f.reads, tc.wantReads)
			}
		})
	}
}

// A preference Sonarr cannot report must not quietly add the series in
// English: the submission fails and Silo retries it.
func TestSubmitSeriesFailsWhenPreferredLanguageUnreadable(t *testing.T) {
	f := &fakeSonarrV5{t: t}
	err := submitV5Series(t, f, nil)
	if err == nil || !strings.Contains(err.Error(), "preferred metadata language") {
		t.Fatalf("error = %v, want the preferred language read to fail", err)
	}
	if f.posted != nil {
		t.Fatalf("posted %v, want nothing added", f.posted)
	}
}

// Sonarr v4 has no per-series language, so the add carries none.
func TestSubmitSeriesSendsNoLanguageOnSonarrV4(t *testing.T) {
	f := &fakeSonarrV5{t: t}
	if err := submitV5Series(t, sonarrV4(f.ServeHTTP), nil); err != nil {
		t.Fatalf("SubmitSeries: %v", err)
	}
	if _, ok := f.posted["language"]; ok {
		t.Fatalf("posted language %v to Sonarr v4, want none", f.posted["language"])
	}
}

func TestListSeriesIntegrationOptionsListsMetadataLanguages(t *testing.T) {
	list := func(t *testing.T, handler http.Handler) []IntegrationLanguage {
		t.Helper()
		server := httptest.NewServer(handler)
		defer server.Close()
		options, err := NewSonarrClient(server.Client()).ListSeriesIntegrationOptions(context.Background(), Instance{
			Kind: "sonarr", BaseURL: server.URL, APIKeyRef: "sonarr-key",
		})
		if err != nil {
			t.Fatalf("ListSeriesIntegrationOptions: %v", err)
		}
		return options.MetadataLanguages
	}

	t.Run("v5", func(t *testing.T) {
		// Sonarr's preferred language comes first so an empty choice keeps
		// Sonarr's default; Any and Unknown are left out.
		got := list(t, &fakeSonarrV5{t: t, settings: `{"preferredMetadataLanguage":4}`})
		want := []IntegrationLanguage{{4, "German"}, {1, "English"}, {8, "Japanese"}}
		if len(got) != len(want) {
			t.Fatalf("languages = %+v, want %+v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("languages = %+v, want %+v", got, want)
			}
		}
	})
	t.Run("v4", func(t *testing.T) {
		f := &fakeSonarrV5{t: t}
		if got := list(t, sonarrV4(f.ServeHTTP)); len(got) != 0 {
			t.Fatalf("languages = %+v, want none from Sonarr v4", got)
		}
	})
}
