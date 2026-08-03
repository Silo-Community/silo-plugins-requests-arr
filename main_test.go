package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
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

func TestAnimeDefaultManifestContract(t *testing.T) {
	if err := validateAnimeDefaultManifest(manifestJSON); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedAnimeDefaultManifestIsRejected(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "missing anime role field",
			mutate: func(data []byte) []byte {
				return bytes.Replace(data, []byte(`"key": "is_anime_default",`), []byte(`"key": "removed_anime_role",`), 1)
			},
		},
		{
			name: "wrong anime section membership",
			mutate: func(data []byte) []byte {
				return bytes.Replace(data, []byte(`"anime_tags",
                  "is_anime_default",
                  "is_anime_default_4k"`), []byte(`"anime_tags"`), 1)
			},
		},
		{
			name: "missing anime 4k show_when",
			mutate: func(data []byte) []byte {
				return replaceLast(data, []byte(`"show_when": [
                  {
                    "field": "is_4k",
                    "equals": [
                      "true"
                    ]
                  }
                ],
                "exclusive_group_field": "service_kind"`), []byte(`"exclusive_group_field": "service_kind"`))
			},
		},
		{
			name: "incorrect anime 4k show_when",
			mutate: func(data []byte) []byte {
				return replaceLast(data, []byte(`"field": "is_4k",
                    "equals": [
                      "true"`), []byte(`"field": "is_4k",
                    "equals": [
                      "false"`))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateAnimeDefaultManifest(tt.mutate(manifestJSON)); err == nil {
				t.Fatalf("validateAnimeDefaultManifest accepted malformed %s", tt.name)
			}
		})
	}
}

func validateAnimeDefaultManifest(data []byte) error {
	m, err := publicmanifest.LoadWithChecksum(data, version)
	if err != nil {
		return fmt.Errorf("LoadWithChecksum: %w", err)
	}
	schema := m.GetCapabilities()[0].GetConfigSchema()[0]
	var schemaEnvelope struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema.GetJsonSchema()), &schemaEnvelope); err != nil {
		return fmt.Errorf("parse json_schema: %w", err)
	}
	schemaFields := schemaEnvelope.Properties
	for _, key := range []string{"is_anime_default", "is_anime_default_4k"} {
		if schemaFields[key].Type != "boolean" {
			return fmt.Errorf("json_schema property %q type = %q, want boolean", key, schemaFields[key].Type)
		}
	}

	fields := make(map[string]*pluginv1.AdminFormField)
	for _, field := range schema.GetAdminForm().GetFields() {
		fields[field.GetKey()] = field
	}
	for _, want := range []struct {
		key         string
		label       string
		description string
	}{
		{key: "is_anime_default", label: "Anime default (HD/1080p)", description: "Prefer this instance for anime HD requests; fall back to the standard HD default when unset."},
		{key: "is_anime_default_4k", label: "Anime default 4K (2160p)", description: "Prefer this instance for anime 4K requests; fall back to the standard 4K default when unset."},
	} {
		field := fields[want.key]
		if field == nil {
			return fmt.Errorf("missing admin field %q", want.key)
		}
		if field.GetLabel() != want.label {
			return fmt.Errorf("%s label = %q, want %q", want.key, field.GetLabel(), want.label)
		}
		if field.GetDescription() != want.description {
			return fmt.Errorf("%s description = %q, want %q", want.key, field.GetDescription(), want.description)
		}
		if field.GetExclusiveGroupField() != "service_kind" {
			return fmt.Errorf("%s exclusive_group_field = %q, want service_kind", want.key, field.GetExclusiveGroupField())
		}
	}

	var anime *pluginv1.AdminFormSection
	for _, section := range schema.GetAdminForm().GetSections() {
		if section.GetKey() == "anime" {
			anime = section
		}
	}
	if anime == nil {
		return fmt.Errorf("anime section is missing")
	}
	for _, key := range []string{"is_anime_default", "is_anime_default_4k"} {
		found := false
		for _, sectionKey := range anime.GetFieldKeys() {
			if sectionKey == key {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("anime section does not contain %q", key)
		}
	}

	fourK := fields["is_anime_default_4k"].GetShowWhen()
	if len(fourK) != 1 || fourK[0].GetField() != "is_4k" || len(fourK[0].GetEquals()) != 1 || fourK[0].GetEquals()[0] != "true" {
		return fmt.Errorf("is_anime_default_4k show_when = %+v, want is_4k=true", fourK)
	}
	return nil
}

func replaceLast(data, old, replacement []byte) []byte {
	index := bytes.LastIndex(data, old)
	if index < 0 {
		return data
	}
	result := make([]byte, 0, len(data)-len(old)+len(replacement))
	result = append(result, data[:index]...)
	result = append(result, replacement...)
	return append(result, data[index+len(old):]...)
}
