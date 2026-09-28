package arr

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"

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
	AddOptions       addSeriesOptions `json:"addOptions,omitempty"`
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
}

type seriesStatisticsSubset struct {
	// EpisodeCount counts only episodes that have aired and are monitored, so it
	// does not grow with an ongoing show's unaired episodes.
	EpisodeCount     int `json:"episodeCount"`
	EpisodeFileCount int `json:"episodeFileCount"`
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
		return resultFromSeries(existing), nil
	}

	series, err := c.lookupSeries(ctx, client, *req.TVDBID)
	if err != nil {
		return FulfillmentResult{}, err
	}
	series.RootFolderPath = integration.RootFolder
	series.QualityProfileID = *integration.QualityProfileID
	series.SeasonFolder = BoolOption(integration.Options, "season_folder", true)
	series.Monitored = BoolOption(integration.Options, "monitored", true)
	series.SeriesType = StringOption(integration.Options, "series_type", "standard")
	series.Tags = integration.Tags
	series.AddOptions = addSeriesOptions{
		Monitor: StringOption(integration.Options, "monitor", DefaultSeriesMonitorPolicy),
		SearchForMissingEpisodes: BoolOption(
			integration.Options,
			"search_for_missing_episodes",
			BoolOption(integration.Options, "search_on_add", true),
		),
		SearchForCutoffUnmetEpisodes: BoolOption(integration.Options, "search_for_cutoff_unmet", false),
	}

	var created seriesResource
	if postErr := client.PostJSON(ctx, "/api/v3/series", series, &created); postErr != nil {
		// The series may have been added between the preflight lookup and POST,
		// or the POST may have succeeded even though its response was lost.
		// Recover either case without hiding a genuine add failure.
		if existing, found, lookupErr := c.findSeriesByTVDBID(ctx, client, *req.TVDBID); lookupErr == nil && found {
			return resultFromSeries(existing), nil
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
	// Imported episodes leave Sonarr's queue, so an empty queue alone cannot say
	// whether the series was fulfilled — see the equivalent note in
	// RadarrClient.CheckMovieStatus. A series has no hasFile, so the closest
	// analogue is "every aired, monitored episode is on disk". Anything short of
	// that stays queued: the queue goes briefly empty between grabs, and
	// completing there would strand a half-downloaded series.
	if len(queues) == 0 {
		series, err := c.seriesByID(ctx, client, seriesID)
		if err != nil {
			return FulfillmentStatus{}, err
		}
		stats := series.Statistics
		if stats.EpisodeCount > 0 && stats.EpisodeFileCount >= stats.EpisodeCount {
			return FulfillmentStatus{
				Status:          StatusCompleted,
				IntegrationKind: "sonarr",
				ExternalID:      strconv.Itoa(seriesID),
				ExternalStatus:  "imported",
			}, nil
		}
	}
	evaluation := EvaluateQueue(queues)
	return StatusFromQueueEvaluation("sonarr", seriesID, evaluation), nil
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
