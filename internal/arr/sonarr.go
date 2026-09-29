package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

// SeriesMonitorPolicies are the Sonarr v4 addOptions.monitor values a
// connection may choose. The policy decides which episodes a new series
// monitors, and so how many episodes search_on_add asks the indexers for:
// "all" searches every aired episode of the series.
var SeriesMonitorPolicies = []string{"all", "future", "recent", "lastSeason", "firstSeason", "pilot"}

// DefaultSeriesMonitorPolicy applies when a connection has no monitor setting.
// It must match the manifest's default_value so the admin form shows the
// policy that is actually sent.
const DefaultSeriesMonitorPolicy = "all"

// IsSeriesMonitorPolicy reports whether policy is one of SeriesMonitorPolicies.
func IsSeriesMonitorPolicy(policy string) bool {
	return slices.Contains(SeriesMonitorPolicies, policy)
}

type SonarrClient struct {
	httpClient *http.Client
}

type seriesResource struct {
	ID               int              `json:"id,omitempty"`
	Title            string           `json:"title,omitempty"`
	TVDBID           int              `json:"tvdbId,omitempty"`
	TMDBID           int              `json:"tmdbId,omitempty"`
	TitleSlug        string           `json:"titleSlug,omitempty"`
	QualityProfileID int              `json:"qualityProfileId,omitempty"`
	RootFolderPath   string           `json:"rootFolderPath,omitempty"`
	SeasonFolder     bool             `json:"seasonFolder"`
	Monitored        bool             `json:"monitored"`
	SeriesType       string           `json:"seriesType,omitempty"`
	Tags             []int            `json:"tags,omitempty"`
	Seasons          []seasonResource `json:"seasons,omitempty"`
	MonitorNewItems  string           `json:"monitorNewItems,omitempty"`
	AddOptions       addSeriesOptions `json:"addOptions,omitempty"`
}

// seasonResource is one entry of a Sonarr series' seasons list.
type seasonResource struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
	// Statistics is read-only; it is nil in what the plugin sends.
	Statistics *seriesStatisticsSubset `json:"statistics,omitempty"`
}

type addSeriesOptions struct {
	Monitor                      string `json:"monitor,omitempty"`
	SearchForMissingEpisodes     bool   `json:"searchForMissingEpisodes"`
	SearchForCutoffUnmetEpisodes bool   `json:"searchForCutoffUnmetEpisodes,omitempty"`
}

// seriesStatusResource is the subset of Sonarr's SeriesResource needed to decide
// whether a series is fulfilled. Kept separate from seriesResource so the add
// payload does not grow read-only fields.
type seriesStatusResource struct {
	ID         int                    `json:"id,omitempty"`
	Statistics seriesStatisticsSubset `json:"statistics"`
	Seasons    []seasonResource       `json:"seasons"`
}

type seriesStatisticsSubset struct {
	// EpisodeCount counts only episodes that have aired and are monitored (or
	// have a file), so it does not grow with an ongoing show's unaired
	// episodes. Sonarr reports it per series and per season.
	EpisodeCount     int `json:"episodeCount"`
	EpisodeFileCount int `json:"episodeFileCount"`
}

// complete reports whether every counted episode is on disk. Zero counted
// episodes is not completion: a freshly added series has none yet.
func (s *seriesStatisticsSubset) complete() bool {
	return s != nil && s.EpisodeCount > 0 && s.EpisodeFileCount >= s.EpisodeCount
}

func NewSonarrClient(httpClient *http.Client) *SonarrClient {
	return &SonarrClient{httpClient: httpClient}
}

func (c *SonarrClient) ListSeriesIntegrationOptions(ctx context.Context, integration Instance) (*IntegrationOptions, error) {
	client := httpclient.New(integration.BaseURL, integration.APIKeyRef, c.httpClient)
	rootFolders, err := ListRootFolders(ctx, client)
	if err != nil {
		return nil, err
	}
	qualityProfiles, err := ListQualityProfiles(ctx, client)
	if err != nil {
		return nil, err
	}
	tags, err := ListTags(ctx, client)
	if err != nil {
		return nil, err
	}
	return &IntegrationOptions{
		Kind:            "sonarr",
		RootFolders:     rootFolders,
		QualityProfiles: qualityProfiles,
		Tags:            tags,
	}, nil
}

// SubmitSeries adds the requested series to Sonarr, or adopts it when Sonarr
// already has it.
//
// A request for the whole series (no seasons) adds it under the connection's
// monitor policy and adopts an existing series as it is. A request that names
// seasons acquires only those: a new series is added with only the requested
// seasons monitored, and an existing one has the requested seasons monitored
// and searched while its other seasons are left as they are. The requested
// seasons replace the monitor policy, which would otherwise pick seasons of
// its own. Repeating a request converges: seasons already monitored stay so,
// and a season with every aired episode on disk is not searched again.
func (c *SonarrClient) SubmitSeries(ctx context.Context, req Request, integration Instance) (FulfillmentResult, error) {
	if req.MediaType != MediaTypeSeries {
		return FulfillmentResult{}, fmt.Errorf("sonarr: request is not a series")
	}
	if integration.QualityProfileID == nil {
		return FulfillmentResult{}, fmt.Errorf("sonarr: quality profile is required")
	}
	if req.TVDBID == nil || *req.TVDBID <= 0 {
		return FulfillmentResult{}, fmt.Errorf("sonarr: tvdb_id is required")
	}

	client := httpclient.New(integration.BaseURL, integration.APIKeyRef, c.httpClient)
	existing, found, err := c.findSeriesByTVDBID(ctx, client, *req.TVDBID)
	if err != nil {
		return FulfillmentResult{}, err
	}
	if found {
		return c.adoptSeries(ctx, client, existing, req, integration)
	}

	series, err := c.lookupSeries(ctx, client, *req.TVDBID)
	if err != nil {
		return FulfillmentResult{}, err
	}
	search := searchOnAdd(integration)
	series.RootFolderPath = integration.RootFolder
	series.QualityProfileID = *integration.QualityProfileID
	series.SeasonFolder = BoolOption(integration.Options, "season_folder", true)
	series.Monitored = BoolOption(integration.Options, "monitored", true)
	series.SeriesType = StringOption(integration.Options, "series_type", "standard")
	series.Tags = integration.Tags
	if len(req.Seasons) > 0 {
		seasons, err := monitorOnlySeasons(series.Seasons, req.Seasons)
		if err != nil {
			return FulfillmentResult{}, err
		}
		// Monitored stays true: an unmonitored series grabs nothing. Leaving
		// addOptions.monitor unset makes Sonarr take each season's monitored
		// flag as sent, and searchForMissingEpisodes then searches only the
		// episodes of those seasons. Seasons Sonarr learns of later are left
		// unmonitored; the host requests them when they are wanted.
		series.Monitored = true
		series.Seasons = seasons
		series.MonitorNewItems = "none"
		series.AddOptions = addSeriesOptions{
			SearchForMissingEpisodes:     search,
			SearchForCutoffUnmetEpisodes: BoolOption(integration.Options, "search_for_cutoff_unmet", false),
		}
	} else {
		// The whole series follows the connection's monitor policy, which
		// decides the seasons itself.
		series.Seasons = nil
		series.MonitorNewItems = ""
		series.AddOptions = addSeriesOptions{
			Monitor:                      StringOption(integration.Options, "monitor", DefaultSeriesMonitorPolicy),
			SearchForMissingEpisodes:     search,
			SearchForCutoffUnmetEpisodes: BoolOption(integration.Options, "search_for_cutoff_unmet", false),
		}
	}

	var created seriesResource
	if postErr := client.PostJSON(ctx, "/api/v3/series", series, &created); postErr != nil {
		// The series may have been added between the preflight lookup and POST,
		// or the POST may have succeeded even though its response was lost.
		// Recover either case without hiding a genuine add failure.
		if existing, found, lookupErr := c.findSeriesByTVDBID(ctx, client, *req.TVDBID); lookupErr == nil && found {
			return c.adoptSeries(ctx, client, existing, req, integration)
		}
		return FulfillmentResult{}, postErr
	}
	if created.ID == 0 {
		// POST accepted but Sonarr returned an empty body. Recover the new
		// series' Sonarr ID by listing series filtered by TVDB ID; without the
		// ID the reconcile loop cannot advance the request.
		if existing, found, lookupErr := c.findSeriesByTVDBID(ctx, client, *req.TVDBID); lookupErr == nil && found {
			return resultFromSeries(existing), nil
		}
		return AcceptedWithoutResponse("sonarr"), nil
	}
	return resultFromSeries(created), nil
}

// searchOnAdd reports whether the connection searches for what it adds.
func searchOnAdd(integration Instance) bool {
	return BoolOption(
		integration.Options,
		"search_for_missing_episodes",
		BoolOption(integration.Options, "search_on_add", true),
	)
}

// monitorOnlySeasons returns the series' seasons with only the requested ones
// monitored. It fails when Sonarr knows none of them, since adding the series
// would then acquire nothing.
func monitorOnlySeasons(available []seasonResource, requested []int) ([]seasonResource, error) {
	out := make([]seasonResource, 0, len(available))
	matched := false
	for _, season := range available {
		monitored := slices.Contains(requested, season.SeasonNumber)
		matched = matched || monitored
		out = append(out, seasonResource{SeasonNumber: season.SeasonNumber, Monitored: monitored})
	}
	if !matched {
		return nil, noRequestedSeasonError(requested)
	}
	return out, nil
}

func noRequestedSeasonError(requested []int) error {
	return fmt.Errorf("sonarr: the series has none of the requested seasons (%s) yet", joinInts(requested))
}

// adoptSeries takes over a series Sonarr already has. A whole-series request
// adopts it unchanged. A season request adds the requested seasons to what the
// series monitors and searches them.
func (c *SonarrClient) adoptSeries(ctx context.Context, client *httpclient.Client, existing seriesResource, req Request, integration Instance) (FulfillmentResult, error) {
	if len(req.Seasons) == 0 {
		return resultFromSeries(existing), nil
	}
	if err := c.addSeasons(ctx, client, existing.ID, req.Seasons, searchOnAdd(integration)); err != nil {
		return FulfillmentResult{}, err
	}
	return resultFromSeries(existing), nil
}

// addSeasons monitors the requested seasons of an existing series, every
// episode in them included, and searches those seasons when search is set. It
// never unmonitors anything. The series is read and written back whole, so
// fields this client does not model survive the update.
func (c *SonarrClient) addSeasons(ctx context.Context, client *httpclient.Client, seriesID int, requested []int, search bool) error {
	path := "/api/v3/series/" + strconv.Itoa(seriesID)
	var raw map[string]any
	if err := client.GetJSON(ctx, path, &raw); err != nil {
		return err
	}
	var current struct {
		Monitored bool             `json:"monitored"`
		Seasons   []seasonResource `json:"seasons"`
	}
	if err := remarshal(raw, &current); err != nil {
		return fmt.Errorf("sonarr: read series %d: %w", seriesID, err)
	}

	changed := !current.Monitored
	known := false
	var toSearch []int
	for _, season := range current.Seasons {
		if !slices.Contains(requested, season.SeasonNumber) {
			continue
		}
		known = true
		changed = changed || !season.Monitored
		// A monitored season's statistics count its aired episodes, so one
		// with all of them on disk has nothing to fetch and a repeated
		// request does not search it again. An unmonitored season's
		// statistics leave out its missing episodes, so it is searched.
		if !season.Monitored || !season.Statistics.complete() {
			toSearch = append(toSearch, season.SeasonNumber)
		}
	}
	if !known {
		return noRequestedSeasonError(requested)
	}

	if changed {
		raw["monitored"] = true
		seasons, _ := raw["seasons"].([]any)
		for _, entry := range seasons {
			season, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if n, ok := season["seasonNumber"].(float64); ok && slices.Contains(requested, int(n)) {
				season["monitored"] = true
			}
		}
		// Sonarr monitors every episode of a season whose flag turns on.
		if err := client.DoJSON(ctx, http.MethodPut, path, raw, nil); err != nil {
			return err
		}
	}

	// A season already monitored can still hold unmonitored episodes (a
	// "future" or "recent" policy leaves the aired ones off), which Sonarr
	// would neither search nor count. Monitor them too.
	if err := c.monitorSeasonEpisodes(ctx, client, seriesID, requested); err != nil {
		return err
	}

	if !search {
		return nil
	}
	for _, seasonNumber := range toSearch {
		command := map[string]any{"name": "SeasonSearch", "seriesId": seriesID, "seasonNumber": seasonNumber}
		if err := client.PostJSON(ctx, "/api/v3/command", command, nil); err != nil {
			return err
		}
	}
	return nil
}

type episodeResource struct {
	ID           int  `json:"id"`
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// monitorSeasonEpisodes monitors the unmonitored episodes of the requested
// seasons.
func (c *SonarrClient) monitorSeasonEpisodes(ctx context.Context, client *httpclient.Client, seriesID int, requested []int) error {
	values := url.Values{}
	values.Set("seriesId", strconv.Itoa(seriesID))
	var episodes []episodeResource
	if err := client.GetJSON(ctx, "/api/v3/episode?"+values.Encode(), &episodes); err != nil {
		return err
	}
	var ids []int
	for _, episode := range episodes {
		if !episode.Monitored && slices.Contains(requested, episode.SeasonNumber) {
			ids = append(ids, episode.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	body := map[string]any{"episodeIds": ids, "monitored": true}
	return client.DoJSON(ctx, http.MethodPut, "/api/v3/episode/monitor", body, nil)
}

func remarshal(from, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, ", ")
}

func (c *SonarrClient) findSeriesByTVDBID(ctx context.Context, client *httpclient.Client, tvdbID int) (seriesResource, bool, error) {
	values := url.Values{}
	values.Set("tvdbId", strconv.Itoa(tvdbID))
	var matches []seriesResource
	if err := client.GetJSON(ctx, "/api/v3/series?"+values.Encode(), &matches); err != nil {
		return seriesResource{}, false, err
	}
	for _, s := range matches {
		if s.ID > 0 && s.TVDBID == tvdbID {
			return s, true, nil
		}
	}
	return seriesResource{}, false, nil
}

func (c *SonarrClient) CheckSeriesStatus(ctx context.Context, req Request, integration Instance) (FulfillmentStatus, error) {
	client := httpclient.New(integration.BaseURL, integration.APIKeyRef, c.httpClient)
	seriesID, _ := strconv.Atoi(req.ExternalID)
	if seriesID <= 0 {
		return FulfillmentStatus{
			Status:          StatusQueued,
			IntegrationKind: "sonarr",
			ExternalStatus:  "external_id_unavailable",
		}, nil
	}

	queues, err := c.queueDetails(ctx, client, seriesID)
	if err != nil {
		return FulfillmentStatus{}, err
	}
	if len(req.Seasons) > 0 {
		// Downloads for seasons the request did not ask for are not its
		// progress, and their failure is not its failure.
		queues = slices.DeleteFunc(queues, func(q QueueResource) bool {
			return q.SeasonNumber != nil && !slices.Contains(req.Seasons, *q.SeasonNumber)
		})
	}
	// Imported episodes leave Sonarr's queue, so an empty queue alone cannot say
	// whether the series was fulfilled — see the equivalent note in
	// RadarrClient.CheckMovieStatus. A series has no hasFile, so the closest
	// analogue is "every aired, monitored episode is on disk". Anything short of
	// that stays queued: the queue goes briefly empty between grabs, and
	// completing there would strand a half-downloaded series. A season request
	// counts only the episodes of its seasons.
	if len(queues) == 0 {
		series, err := c.seriesByID(ctx, client, seriesID)
		if err != nil {
			return FulfillmentStatus{}, err
		}
		stats := series.Statistics
		if len(req.Seasons) > 0 {
			stats = seriesStatisticsSubset{}
			for _, season := range series.Seasons {
				if season.Statistics != nil && slices.Contains(req.Seasons, season.SeasonNumber) {
					stats.EpisodeCount += season.Statistics.EpisodeCount
					stats.EpisodeFileCount += season.Statistics.EpisodeFileCount
				}
			}
		}
		if stats.complete() {
			return FulfillmentStatus{
				Status:          StatusCompleted,
				IntegrationKind: "sonarr",
				ExternalID:      strconv.Itoa(seriesID),
				ExternalStatus:  "imported",
			}, nil
		}
	}
	return statusFromQueue(ctx, client, "sonarr", seriesID, queues), nil
}

func (c *SonarrClient) seriesByID(ctx context.Context, client *httpclient.Client, seriesID int) (seriesStatusResource, error) {
	var series seriesStatusResource
	if err := client.GetJSON(ctx, "/api/v3/series/"+strconv.Itoa(seriesID), &series); err != nil {
		return seriesStatusResource{}, err
	}
	return series, nil
}

func (c *SonarrClient) lookupSeries(ctx context.Context, client *httpclient.Client, tvdbID int) (seriesResource, error) {
	values := url.Values{}
	values.Set("term", "tvdb:"+strconv.Itoa(tvdbID))
	var matches []seriesResource
	if err := client.GetJSON(ctx, "/api/v3/series/lookup?"+values.Encode(), &matches); err != nil {
		return seriesResource{}, err
	}
	for _, match := range matches {
		if match.TVDBID == tvdbID {
			return match, nil
		}
	}
	return seriesResource{}, fmt.Errorf("sonarr: no series found for tvdb_id %d", tvdbID)
}

func (c *SonarrClient) queueDetails(ctx context.Context, client *httpclient.Client, seriesID int) ([]QueueResource, error) {
	values := url.Values{}
	values.Set("seriesId", strconv.Itoa(seriesID))
	var queues []QueueResource
	if err := client.GetJSON(ctx, "/api/v3/queue/details?"+values.Encode(), &queues); err != nil {
		return nil, err
	}
	return queues, nil
}

func resultFromSeries(series seriesResource) FulfillmentResult {
	externalID := ""
	if series.ID > 0 {
		externalID = strconv.Itoa(series.ID)
	}
	return FulfillmentResult{
		IntegrationKind: "sonarr",
		ExternalID:      externalID,
		ExternalStatus:  "queued",
	}
}
