package arr

import "testing"

func TestRouteTargetsPreservesLegacyRouting(t *testing.T) {
	tests := []struct {
		name      string
		req       Request
		qualities []RequestedQuality
		instances []Instance
		wantIDs   []string
		wantAnime []bool
	}{
		{
			name: "non anime movie routes HD and 4K by legacy roles",
			req:  Request{MediaType: MediaTypeMovie},
			qualities: []RequestedQuality{
				{ID: "1080p"},
				{ID: "2160p", Is4K: true},
			},
			instances: []Instance{
				{ID: "hd", Kind: "radarr", Enabled: true, IsDefault: true},
				{ID: "hd-second", Kind: "radarr", Enabled: true, IsDefault: true},
				{ID: "uhd", Kind: "radarr", Enabled: true, IsDefault4K: true},
			},
			wantIDs:   []string{"hd", "uhd"},
			wantAnime: []bool{false, false},
		},
		{
			name:      "wrong kind is omitted",
			req:       Request{MediaType: MediaTypeMovie},
			qualities: []RequestedQuality{{ID: "1080p"}},
			instances: []Instance{
				{ID: "sonarr-hd", Kind: "sonarr", Enabled: true, IsDefault: true},
			},
		},
		{
			name:      "tier without a default is omitted",
			req:       Request{MediaType: MediaTypeMovie},
			qualities: []RequestedQuality{{ID: "2160p", Is4K: true}},
			instances: []Instance{
				{ID: "hd", Kind: "radarr", Enabled: true, IsDefault: true},
			},
		},
		{
			name:      "legacy anime enabled selects anime overlays",
			req:       Request{MediaType: MediaTypeSeries, IsAnime: true},
			qualities: []RequestedQuality{{ID: "1080p"}},
			instances: []Instance{
				{ID: "legacy", Kind: "sonarr", Enabled: true, IsDefault: true, AnimeEnabled: true},
			},
			wantIDs:   []string{"legacy"},
			wantAnime: []bool{true},
		},
		{
			name:      "legacy anime disabled does not select anime overlays",
			req:       Request{MediaType: MediaTypeSeries, IsAnime: true},
			qualities: []RequestedQuality{{ID: "1080p"}},
			instances: []Instance{
				{ID: "legacy", Kind: "sonarr", Enabled: true, IsDefault: true},
			},
			wantIDs:   []string{"legacy"},
			wantAnime: []bool{false},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given: the configured instances and host request in the test case.
			// When: routing the requested qualities.
			planned := RouteTargets(test.req, test.qualities, test.instances)

			// Then: every emitted target matches the legacy behavior exactly.
			if len(planned) != len(test.wantIDs) {
				t.Fatalf("target count = %d, want %d", len(planned), len(test.wantIDs))
			}
			for i, target := range planned {
				if target.Instance.ID != test.wantIDs[i] || target.IsAnime != test.wantAnime[i] || target.Quality != test.qualities[i].ID {
					t.Fatalf("target[%d] = %+v, want id %q, anime %t, quality %q", i, target, test.wantIDs[i], test.wantAnime[i], test.qualities[i].ID)
				}
			}
		})
	}
}

func TestRouteTargetsPrefersAnimeRoles(t *testing.T) {
	tests := []struct {
		name      string
		req       Request
		qualities []RequestedQuality
		instances []Instance
		wantIDs   []string
		wantAnime []bool
	}{
		{
			name: "anime Sonarr request prefers HD and 4K anime roles",
			req:  Request{MediaType: MediaTypeSeries, IsAnime: true},
			qualities: []RequestedQuality{
				{ID: "1080p"},
				{ID: "2160p", Is4K: true},
			},
			instances: []Instance{
				{ID: "legacy-hd", Kind: "sonarr", Enabled: true, IsDefault: true, AnimeEnabled: true},
				{ID: "anime-hd", Kind: "sonarr", Enabled: true, IsAnimeDefault: true},
				{ID: "anime-hd-second", Kind: "sonarr", Enabled: true, IsAnimeDefault: true},
				{ID: "legacy-4k", Kind: "sonarr", Enabled: true, IsDefault4K: true, AnimeEnabled: true},
				{ID: "anime-4k", Kind: "sonarr", Enabled: true, IsAnimeDefault4K: true},
			},
			wantIDs:   []string{"anime-hd", "anime-4k"},
			wantAnime: []bool{true, true},
		},
		{
			name: "anime Radarr request prefers HD and 4K anime roles",
			req:  Request{MediaType: MediaTypeMovie, IsAnime: true},
			qualities: []RequestedQuality{
				{ID: "1080p"},
				{ID: "2160p", Is4K: true},
			},
			instances: []Instance{
				{ID: "legacy-hd", Kind: "radarr", Enabled: true, IsDefault: true},
				{ID: "anime-hd", Kind: "radarr", Enabled: true, IsAnimeDefault: true},
				{ID: "legacy-4k", Kind: "radarr", Enabled: true, IsDefault4K: true},
				{ID: "anime-4k", Kind: "radarr", Enabled: true, IsAnimeDefault4K: true},
			},
			wantIDs:   []string{"anime-hd", "anime-4k"},
			wantAnime: []bool{true, true},
		},
		{
			name:      "non anime request ignores anime role",
			req:       Request{MediaType: MediaTypeSeries},
			qualities: []RequestedQuality{{ID: "1080p"}},
			instances: []Instance{
				{ID: "anime", Kind: "sonarr", Enabled: true, IsAnimeDefault: true},
				{ID: "legacy", Kind: "sonarr", Enabled: true, IsDefault: true},
			},
			wantIDs:   []string{"legacy"},
			wantAnime: []bool{false},
		},
		{
			name: "anime roles fall back independently by tier",
			req:  Request{MediaType: MediaTypeSeries, IsAnime: true},
			qualities: []RequestedQuality{
				{ID: "1080p"},
				{ID: "2160p", Is4K: true},
			},
			instances: []Instance{
				{ID: "anime-hd", Kind: "sonarr", Enabled: true, IsAnimeDefault: true},
				{ID: "legacy-4k", Kind: "sonarr", Enabled: true, IsDefault4K: true},
			},
			wantIDs:   []string{"anime-hd", "legacy-4k"},
			wantAnime: []bool{true, false},
		},
		{
			name:      "anime role requires enabled matching kind and tier",
			req:       Request{MediaType: MediaTypeMovie, IsAnime: true},
			qualities: []RequestedQuality{{ID: "2160p", Is4K: true}},
			instances: []Instance{
				{ID: "wrong-kind", Kind: "sonarr", Enabled: true, IsAnimeDefault4K: true},
				{ID: "wrong-tier", Kind: "radarr", Enabled: true, IsAnimeDefault: true},
				{ID: "disabled", Kind: "radarr", IsAnimeDefault4K: true},
				{ID: "fallback", Kind: "radarr", Enabled: true, IsDefault4K: true, AnimeEnabled: true},
			},
			wantIDs:   []string{"fallback"},
			wantAnime: []bool{true},
		},
		{
			name:      "missing role emits no target",
			req:       Request{MediaType: MediaTypeMovie, IsAnime: true},
			qualities: []RequestedQuality{{ID: "1080p"}},
			instances: []Instance{
				{ID: "wrong-tier", Kind: "radarr", Enabled: true, IsDefault4K: true},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given: configured standard and anime-role instances.
			// When: an anime or non-anime request is routed.
			planned := RouteTargets(test.req, test.qualities, test.instances)

			// Then: selection and overlay intent match the role contract.
			if len(planned) != len(test.wantIDs) {
				t.Fatalf("target count = %d, want %d", len(planned), len(test.wantIDs))
			}
			for i, target := range planned {
				if target.Instance.ID != test.wantIDs[i] || target.IsAnime != test.wantAnime[i] {
					t.Fatalf("target[%d] = %+v, want id %q and anime %t", i, target, test.wantIDs[i], test.wantAnime[i])
				}
			}
		})
	}
}

func TestResolveInstanceAppliesAnimeOverlays(t *testing.T) {
	standardProfile := 1
	animeProfile := 2
	tests := []struct {
		name        string
		planned     PlannedTarget
		wantRoot    string
		wantProfile *int
		wantTags    []int
		wantSeries  any
	}{
		{
			name: "Sonarr retains blank anime overlays and sets series type",
			planned: PlannedTarget{Instance: Instance{
				Kind:             "sonarr",
				RootFolder:       "/series",
				QualityProfileID: &standardProfile,
				Tags:             []int{1},
				Options:          map[string]any{"season_folder": true},
			}, IsAnime: true},
			wantRoot:    "/series",
			wantProfile: &standardProfile,
			wantTags:    []int{1},
			wantSeries:  "anime",
		},
		{
			name: "Sonarr applies populated anime overlays",
			planned: PlannedTarget{Instance: Instance{
				Kind:                  "sonarr",
				RootFolder:            "/series",
				QualityProfileID:      &standardProfile,
				Tags:                  []int{1},
				AnimeRootFolder:       "/anime",
				AnimeQualityProfileID: &animeProfile,
				AnimeTags:             []int{2},
			}, IsAnime: true},
			wantRoot:    "/anime",
			wantProfile: &animeProfile,
			wantTags:    []int{2},
			wantSeries:  "anime",
		},
		{
			name: "Radarr does not add a series type",
			planned: PlannedTarget{Instance: Instance{
				Kind:    "radarr",
				Options: map[string]any{"search_on_add": true},
			}, IsAnime: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given: a planned target with sparse or populated anime overlays.
			// When: resolving the submission instance.
			resolved := ResolveInstance(test.planned)

			// Then: configured overlays inherit correctly without mutating options.
			if resolved.RootFolder != test.wantRoot || resolved.QualityProfileID != test.wantProfile || len(resolved.Tags) != len(test.wantTags) {
				t.Fatalf("resolved = %+v, want root %q, profile %v, tags %v", resolved, test.wantRoot, test.wantProfile, test.wantTags)
			}
			for i, tag := range resolved.Tags {
				if tag != test.wantTags[i] {
					t.Fatalf("tags = %v, want %v", resolved.Tags, test.wantTags)
				}
			}
			if got := resolved.Options["series_type"]; got != test.wantSeries {
				t.Fatalf("series_type = %v, want %v", got, test.wantSeries)
			}
			resolved.Options["mutated"] = true
			if test.planned.Instance.Options != nil && test.planned.Instance.Options["mutated"] != nil {
				t.Fatal("ResolveInstance mutated the planned instance options")
			}
		})
	}
}
