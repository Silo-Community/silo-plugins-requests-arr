// Package arr contains the Sonarr/Radarr fulfillment logic, relocated from the
// silo-server host. It depends only on plugin-local types; config arrives from
// the host per call as a parsed Instance.
//
// Field shapes deliberately mirror the host's mediarequests types (rather than
// the Task 6 template) so the copied radarr/sonarr/routing logic and their
// tests compile with minimal edits:
//   - Request.TVDBID is *int (Sonarr submit dereferences it and the tests pass
//     &tvdbID).
//   - Instance.APIKeyRef (not APIKey) — the copied clients and tests reference
//     integration.APIKeyRef.
//   - Instance.Enabled is present (routing.go reads it).
//   - FulfillmentResult / FulfillmentStatus carry IntegrationKind, and
//     FulfillmentStatus carries Outcome (with the Outcome type + OutcomeFailed
//     const) — the copied resources.go / radarr / sonarr code and tests set
//     these fields.
package arr

type MediaType string

const (
	MediaTypeMovie  MediaType = "movie"
	MediaTypeSeries MediaType = "series"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusDownloading Status = "downloading"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
)

// Outcome is the terminal-vs-active classification carried alongside Status.
type Outcome string

const (
	OutcomeFailed Outcome = "failed"
)

// Request is the per-target view the arr submitters need.
type Request struct {
	MediaType MediaType
	TMDBID    int
	TVDBID    *int
	Title     string
	Year      int
	IsAnime   bool
	// Seasons are the season numbers a series request asks for, sorted and
	// without duplicates; 0 is Specials and none means the whole series.
	Seasons    []int
	ExternalID string // set by CheckStatus probes
}

// Instance is one resolved arr connection: generic host fields plus the parsed plugin_config.
type Instance struct {
	ID                    string
	Kind                  string // "radarr" | "sonarr" (from config service_kind)
	Enabled               bool
	BaseURL               string
	APIKeyRef             string
	RootFolder            string
	QualityProfileID      *int
	Tags                  []int
	IsDefault             bool
	IsDefault4K           bool
	Is4K                  bool
	AnimeEnabled          bool
	AnimeRootFolder       string
	AnimeQualityProfileID *int
	AnimeTags             []int
	Options               map[string]any // search_on_add, minimum_availability, series_type, season_folder, monitor
}

type FulfillmentResult struct {
	IntegrationKind string
	ExternalID      string
	ExternalStatus  string
}

type FulfillmentStatus struct {
	Status          Status
	Outcome         Outcome
	IntegrationKind string
	ExternalID      string
	ExternalStatus  string
	Message         string
	// Progress is set while the target is queued or downloading and the
	// service has queue items for it.
	Progress *Progress
}

type IntegrationRootFolder struct {
	Path       string
	FreeSpace  int64
	TotalSpace int64
	Accessible bool
}
type IntegrationQualityProfile struct {
	ID   int
	Name string
}
type IntegrationTag struct {
	ID    int
	Label string
}
type IntegrationOptions struct {
	Kind            string
	RootFolders     []IntegrationRootFolder
	QualityProfiles []IntegrationQualityProfile
	Tags            []IntegrationTag
}
