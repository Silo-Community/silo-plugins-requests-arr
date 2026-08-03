package router

import (
	"strconv"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Community/silo-plugins-requests-arr/internal/arr"
)

// instanceFromConnection parses a host-supplied RouterConnection into the
// plugin-local arr.Instance. The host sends the API key as a resolved value in
// ApiKey (mapped to APIKeyRef) and the per-instance plugin_config as a struct.
// structpb decodes JSON numbers as float64, so numeric helpers narrow to int.
func instanceFromConnection(c *pluginv1.RouterConnection) arr.Instance {
	cfg := map[string]any{}
	if c.GetConfig() != nil {
		cfg = c.GetConfig().AsMap()
	}
	in := arr.Instance{
		ID:                    c.GetId(),
		BaseURL:               c.GetBaseUrl(),
		APIKeyRef:             c.GetApiKey(),
		Kind:                  getString(cfg, "service_kind"),
		RootFolder:            getString(cfg, "root_folder"),
		QualityProfileID:      getIntPtr(cfg, "quality_profile_id"),
		Tags:                  getIntSlice(cfg, "tags"),
		IsDefault:             getBool(cfg, "is_default"),
		IsDefault4K:           getBool(cfg, "is_default_4k"),
		Is4K:                  getBool(cfg, "is_4k"),
		IsAnimeDefault:        getBool(cfg, "is_anime_default"),
		IsAnimeDefault4K:      getBool(cfg, "is_anime_default_4k"),
		AnimeEnabled:          getBool(cfg, "anime_enabled"),
		AnimeRootFolder:       getString(cfg, "anime_root_folder"),
		AnimeQualityProfileID: getIntPtr(cfg, "anime_quality_profile_id"),
		AnimeTags:             getIntSlice(cfg, "anime_tags"),
		// The host only sends enabled connections; routing additionally gates on
		// IsDefault / IsDefault4K so disabled tiers still yield no target.
		Enabled: true,
		Options: map[string]any{},
	}
	for _, k := range []string{"search_on_add", "minimum_availability", "series_type", "season_folder"} {
		if v, ok := cfg[k]; ok {
			in.Options[k] = v
		}
	}
	return in
}

func getString(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getBool(m map[string]any, k string) bool {
	if v, ok := m[k]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func getIntPtr(m map[string]any, k string) *int {
	v, ok := m[k]
	if !ok {
		return nil
	}
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case int:
		return &n
	case string:
		i := atoiSafe(n)
		return &i
	}
	return nil
}

func getIntSlice(m map[string]any, k string) []int {
	v, ok := m[k]
	if !ok {
		return nil
	}
	arrVal, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(arrVal))
	for _, e := range arrVal {
		switch n := e.(type) {
		case float64:
			out = append(out, int(n))
		case int:
			out = append(out, n)
		case string:
			out = append(out, atoiSafe(n))
		}
	}
	return out
}

func atoiSafe(s string) int { n, _ := strconv.Atoi(s); return n }
func itoa(n int) string     { return strconv.Itoa(n) }
