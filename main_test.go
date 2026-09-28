package main

import (
	"slices"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"

	"github.com/Silo-Community/silo-plugins-requests-arr/internal/arr"
)

func TestAdminFormLayout(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	af := m.GetCapabilities()[0].GetConfigSchema()[0].GetAdminForm()

	var library, anime *pluginv1.AdminFormSection
	for _, s := range af.GetSections() {
		switch s.GetKey() {
		case "library":
			library = s
		case "anime":
			anime = s
		}
	}
	if library == nil || anime == nil {
		t.Fatalf("expected both library and anime sections, got %+v", af.GetSections())
	}

	// Library: collapsible, collapsed by default, and no longer owns the gate toggle.
	if !library.GetCollapsible() || !library.GetCollapsedDefault() {
		t.Errorf("library: collapsible=%v collapsed_default=%v, want both true", library.GetCollapsible(), library.GetCollapsedDefault())
	}
	for _, k := range library.GetFieldKeys() {
		if k == "anime_enabled" {
			t.Errorf("anime_enabled must not be in the library section")
		}
	}

	// Anime: always visible (no section-level show_when, not collapsible) and
	// gated by anime_enabled as its first field.
	if anime.GetCollapsible() {
		t.Errorf("anime section must not be collapsible (the gate toggle must stay visible)")
	}
	if len(anime.GetShowWhen()) != 0 {
		t.Errorf("anime section must not carry a section-level show_when, got %+v", anime.GetShowWhen())
	}
	keys := anime.GetFieldKeys()
	if len(keys) == 0 || keys[0] != "anime_enabled" {
		t.Errorf("anime section must list anime_enabled first, got %v", keys)
	}
}

func TestEmbeddedManifestLoads(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	if m.GetPluginId() != "silo.requests.arr" {
		t.Fatalf("plugin_id = %q", m.GetPluginId())
	}
	if len(m.GetCapabilities()) != 1 || m.GetCapabilities()[0].GetType() != "request_router.v1" {
		t.Fatalf("expected one request_router.v1 capability, got %+v", m.GetCapabilities())
	}
}

// The monitor select must offer exactly the policies the plugin accepts, and
// default to the policy the plugin falls back to, so the form never shows a
// policy other than the one Sonarr receives.
func TestMonitorFieldMatchesSeriesMonitorPolicies(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	var field *pluginv1.AdminFormField
	for _, f := range m.GetCapabilities()[0].GetConfigSchema()[0].GetAdminForm().GetFields() {
		if f.GetKey() == "monitor" {
			field = f
		}
	}
	if field == nil {
		t.Fatal("manifest has no monitor field")
	}
	if got := field.GetDefaultValue().GetStringValue(); got != arr.DefaultSeriesMonitorPolicy {
		t.Errorf("monitor default_value = %q, want %q", got, arr.DefaultSeriesMonitorPolicy)
	}
	var values []string
	for _, o := range field.GetOptions() {
		values = append(values, o.GetValue())
	}
	if !slices.Equal(values, arr.SeriesMonitorPolicies) {
		t.Errorf("monitor options = %v, want %v", values, arr.SeriesMonitorPolicies)
	}
}

// The host sends a request for only the missing seasons of a series it already
// has only to plugins that declare supports_seasons, and it reads the flag from
// the stored manifest. The manifest loader drops unknown keys, so a misspelt
// key would silently turn the feature off.
func TestManifestDeclaresSeasonSupport(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	if !m.GetCapabilities()[0].GetRequestRouter().GetSupportsSeasons() {
		t.Fatalf("request_router.supports_seasons = false, want true")
	}
}

// The host polls a request router for download progress every minute only when
// its stored manifest declares reports_download_progress.
func TestManifestDeclaresDownloadProgress(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	if !m.GetCapabilities()[0].GetRequestRouter().GetReportsDownloadProgress() {
		t.Fatalf("request_router.reports_download_progress = false, want true")
	}
}
