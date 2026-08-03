package router

import (
	"reflect"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Community/silo-plugins-requests-arr/internal/arr"
	"google.golang.org/protobuf/types/known/structpb"
)

func connectionWithConfig(t *testing.T, cfg map[string]any) *pluginv1.RouterConnection {
	t.Helper()
	config, err := structpb.NewStruct(cfg)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return &pluginv1.RouterConnection{
		Id:      "anime-1",
		BaseUrl: "http://sonarr.example",
		ApiKey:  "resolved-api-key",
		Config:  config,
	}
}

func TestInstanceFromConnectionAnimeDefaults(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]any
		wantAnime   bool
		wantAnime4K bool
	}{
		{
			name: "true",
			config: map[string]any{
				"is_anime_default":    true,
				"is_anime_default_4k": true,
			},
			wantAnime:   true,
			wantAnime4K: true,
		},
		{
			name: "false",
			config: map[string]any{
				"is_anime_default":    false,
				"is_anime_default_4k": false,
			},
		},
		{
			name: "HD anime default only",
			config: map[string]any{
				"is_anime_default":    true,
				"is_anime_default_4k": false,
			},
			wantAnime: true,
		},
		{
			name: "4K anime default only",
			config: map[string]any{
				"is_anime_default":    false,
				"is_anime_default_4k": true,
			},
			wantAnime4K: true,
		},
		{
			name: "missing",
			config: map[string]any{
				"service_kind": "sonarr",
			},
		},
		{
			name: "wrong type",
			config: map[string]any{
				"is_anime_default":    "true",
				"is_anime_default_4k": 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := instanceFromConnection(connectionWithConfig(t, tt.config))
			if got.IsAnimeDefault != tt.wantAnime {
				t.Fatalf("IsAnimeDefault: want %t, got %t", tt.wantAnime, got.IsAnimeDefault)
			}
			if got.IsAnimeDefault4K != tt.wantAnime4K {
				t.Fatalf("IsAnimeDefault4K: want %t, got %t", tt.wantAnime4K, got.IsAnimeDefault4K)
			}
		})
	}
}

func TestInstanceFromConnectionPreservesLegacyFields(t *testing.T) {
	got := instanceFromConnection(connectionWithConfig(t, map[string]any{
		"service_kind":             "sonarr",
		"root_folder":              "/tv",
		"quality_profile_id":       float64(7),
		"tags":                     []any{float64(2), float64(5)},
		"is_default":               true,
		"is_default_4k":            true,
		"is_4k":                    true,
		"anime_enabled":            true,
		"anime_root_folder":        "/anime",
		"anime_quality_profile_id": float64(9),
		"anime_tags":               []any{float64(11)},
		"search_on_add":            true,
		"minimum_availability":     "released",
		"series_type":              "standard",
		"season_folder":            false,
		"ignored_option":           "ignored",
	}))

	wantQualityProfileID := 7
	wantAnimeQualityProfileID := 9
	want := arr.Instance{
		ID:                    "anime-1",
		Kind:                  "sonarr",
		Enabled:               true,
		BaseURL:               "http://sonarr.example",
		APIKeyRef:             "resolved-api-key",
		RootFolder:            "/tv",
		QualityProfileID:      &wantQualityProfileID,
		Tags:                  []int{2, 5},
		IsDefault:             true,
		IsDefault4K:           true,
		Is4K:                  true,
		AnimeEnabled:          true,
		AnimeRootFolder:       "/anime",
		AnimeQualityProfileID: &wantAnimeQualityProfileID,
		AnimeTags:             []int{11},
		Options: map[string]any{
			"search_on_add":        true,
			"minimum_availability": "released",
			"series_type":          "standard",
			"season_folder":        false,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("instanceFromConnection changed legacy parsing:\nwant: %#v\n got: %#v", want, got)
	}
}
