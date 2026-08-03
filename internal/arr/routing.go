package arr

// PlannedTarget is a routing decision: which instance, at which quality, and
// whether anime overlays apply. Quality carries the proto-facing identity
// string ("1080p" / "2160p") so the router can echo it straight back to the host.
type PlannedTarget struct {
	Instance Instance
	Quality  string
	IsAnime  bool
}

// RequestedQuality is one host-requested tier: its identity string plus whether
// it is the 4K tier (the host stamps is4k; the plugin no longer parses the id).
type RequestedQuality struct {
	ID   string
	Is4K bool
}

// RouteTargets maps the host-requested qualities onto configured instances for
// the request's kind (radarr for movies, sonarr otherwise).
//
// For each requested quality an anime request first selects the enabled anime
// default for that tier — IsAnimeDefault for HD, IsAnimeDefault4K for 4K — then
// falls back to the corresponding standard default. Non-anime requests use
// standard defaults directly. It emits one PlannedTarget per quality that has a
// matching instance.
// Qualities with no matching instance are omitted, so an unconfigured tier
// silently yields no target rather than an error.
func RouteTargets(req Request, qualities []RequestedQuality, instances []Instance) []PlannedTarget {
	wantKind := "radarr"
	if req.MediaType != MediaTypeMovie {
		wantKind = "sonarr"
	}

	var targets []PlannedTarget
	for _, q := range qualities {
		var match *Instance
		selectedAnimeRole := false
		if req.IsAnime {
			for i := range instances {
				in := &instances[i]
				if !in.Enabled || in.Kind != wantKind {
					continue
				}
				if (!q.Is4K && in.IsAnimeDefault) || (q.Is4K && in.IsAnimeDefault4K) {
					match = in
					selectedAnimeRole = true
					break
				}
			}
		}

		if match == nil {
			for i := range instances {
				in := &instances[i]
				if !in.Enabled || in.Kind != wantKind {
					continue
				}
				if q.Is4K {
					if in.IsDefault4K {
						match = in
					}
				} else {
					if in.IsDefault {
						match = in
					}
				}
				if match != nil {
					break
				}
			}
		}
		if match == nil {
			continue
		}
		targets = append(targets, PlannedTarget{
			Instance: *match,
			Quality:  q.ID,
			IsAnime:  selectedAnimeRole || (req.IsAnime && match.AnimeEnabled),
		})
	}
	return targets
}

// ResolveInstance returns a copy of the planned target's instance with root
// folder / quality profile / tags (and Sonarr series_type) set for standard vs
// anime fulfillment.
func ResolveInstance(pt PlannedTarget) Instance {
	in := pt.Instance
	if in.Options == nil {
		in.Options = map[string]any{}
	} else {
		clone := make(map[string]any, len(in.Options))
		for k, v := range in.Options {
			clone[k] = v
		}
		in.Options = clone
	}
	if pt.IsAnime {
		// Anime fields are overrides: only replace the standard value when the
		// anime counterpart is set, so an admin can enable anime detection while
		// reusing the standard root folder / quality profile / tags for any field
		// they leave blank (rather than clearing them into an invalid submission).
		if in.AnimeRootFolder != "" {
			in.RootFolder = in.AnimeRootFolder
		}
		if in.AnimeQualityProfileID != nil {
			in.QualityProfileID = in.AnimeQualityProfileID
		}
		if len(in.AnimeTags) > 0 {
			in.Tags = in.AnimeTags
		}
		if in.Kind == "sonarr" {
			in.Options["series_type"] = "anime"
		}
	}
	return in
}
