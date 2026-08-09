package arr

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

type RadarrClient struct {
	httpClient *http.Client
}

type movieResource struct {
	ID                  int             `json:"id,omitempty"`
	Title               string          `json:"title,omitempty"`
	TMDBID              int             `json:"tmdbId,omitempty"`
	Year                int             `json:"year,omitempty"`
	TitleSlug           string          `json:"titleSlug,omitempty"`
	QualityProfileID    int             `json:"qualityProfileId,omitempty"`
	RootFolderPath      string          `json:"rootFolderPath,omitempty"`
	Monitored           bool            `json:"monitored"`
	MinimumAvailability string          `json:"minimumAvailability,omitempty"`
	Tags                []int           `json:"tags,omitempty"`
	AddOptions          addMovieOptions `json:"addOptions,omitempty"`
}

type addMovieOptions struct {
	SearchForMovie bool   `json:"searchForMovie"`
	Monitor        string `json:"monitor,omitempty"`
}

// movieStatusResource is the subset of Radarr's MovieResource needed to decide
// whether a movie is fulfilled. It is a separate type from movieResource so the
// add payload does not grow read-only fields.
type movieStatusResource struct {
	ID      int  `json:"id,omitempty"`
	HasFile bool `json:"hasFile"`
}

func NewRadarrClient(httpClient *http.Client) *RadarrClient {
	return &RadarrClient{httpClient: httpClient}
}

func (c *RadarrClient) ListMovieIntegrationOptions(ctx context.Context, integration Instance) (*IntegrationOptions, error) {
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
		Kind:            "radarr",
		RootFolders:     rootFolders,
		QualityProfiles: qualityProfiles,
		Tags:            tags,
	}, nil
}

func (c *RadarrClient) SubmitMovie(ctx context.Context, req Request, integration Instance) (FulfillmentResult, error) {
	if req.MediaType != MediaTypeMovie {
		return FulfillmentResult{}, fmt.Errorf("radarr: request is not a movie")
	}
	if integration.QualityProfileID == nil {
		return FulfillmentResult{}, fmt.Errorf("radarr: quality profile is required")
	}

	client := httpclient.New(integration.BaseURL, integration.APIKeyRef, c.httpClient)
	existing, found, err := c.findMovieByTMDBID(ctx, client, req.TMDBID)
	if err != nil {
		return FulfillmentResult{}, err
	}
	if found {
		return resultFromMovie(existing), nil
	}

	movie, err := c.lookupMovie(ctx, client, req.TMDBID)
	if err != nil {
		return FulfillmentResult{}, err
	}
	movie.RootFolderPath = integration.RootFolder
	movie.QualityProfileID = *integration.QualityProfileID
	movie.Monitored = BoolOption(integration.Options, "monitored", true)
	movie.MinimumAvailability = StringOption(integration.Options, "minimum_availability", "released")
	movie.Tags = integration.Tags
	movie.AddOptions = addMovieOptions{
		SearchForMovie: BoolOption(
			integration.Options,
			"search_for_movie",
			BoolOption(integration.Options, "search_on_add", true),
		),
		Monitor: StringOption(integration.Options, "monitor", "movieOnly"),
	}

	var created movieResource
	if postErr := client.PostJSON(ctx, "/api/v3/movie", movie, &created); postErr != nil {
		// The movie may have been added between the preflight lookup and POST,
		// or the POST may have succeeded even though its response was lost.
		// Recover either case without hiding a genuine add failure.
		if existing, found, lookupErr := c.findMovieByTMDBID(ctx, client, req.TMDBID); lookupErr == nil && found {
			return resultFromMovie(existing), nil
		}
		return FulfillmentResult{}, postErr
	}
	if created.ID == 0 {
		// POST accepted but Radarr returned an empty body. Recover the new
		// movie's Radarr ID by listing movies filtered by TMDB ID; without the
		// ID the reconcile loop cannot advance the request.
		if existing, found, lookupErr := c.findMovieByTMDBID(ctx, client, req.TMDBID); lookupErr == nil && found {
			return resultFromMovie(existing), nil
		}
		return AcceptedWithoutResponse("radarr"), nil
	}
	return resultFromMovie(created), nil
}

func (c *RadarrClient) findMovieByTMDBID(ctx context.Context, client *httpclient.Client, tmdbID int) (movieResource, bool, error) {
	values := url.Values{}
	values.Set("tmdbId", strconv.Itoa(tmdbID))
	var matches []movieResource
	if err := client.GetJSON(ctx, "/api/v3/movie?"+values.Encode(), &matches); err != nil {
		return movieResource{}, false, err
	}
	for _, m := range matches {
		if m.ID > 0 && m.TMDBID == tmdbID {
			return m, true, nil
		}
	}
	return movieResource{}, false, nil
}

func (c *RadarrClient) CheckMovieStatus(ctx context.Context, req Request, integration Instance) (FulfillmentStatus, error) {
	client := httpclient.New(integration.BaseURL, integration.APIKeyRef, c.httpClient)
	movieID, _ := strconv.Atoi(req.ExternalID)
	if movieID <= 0 {
		return FulfillmentStatus{
			Status:          StatusQueued,
			IntegrationKind: "radarr",
			ExternalStatus:  "external_id_unavailable",
		}, nil
	}

	queues, err := c.queueDetails(ctx, client, movieID)
	if err != nil {
		return FulfillmentStatus{}, err
	}
	// An imported movie leaves Radarr's queue entirely, so an empty queue is
	// ambiguous: either nothing was ever grabbed, or the download finished and
	// was imported. Only the movie record separates the two. Without this the
	// queue evaluation reports "queued" for a fulfilled request forever, and the
	// host never sees a target reach a terminal state.
	if len(queues) == 0 {
		movie, err := c.movieByID(ctx, client, movieID)
		if err != nil {
			return FulfillmentStatus{}, err
		}
		if movie.HasFile {
			return FulfillmentStatus{
				Status:          StatusCompleted,
				IntegrationKind: "radarr",
				ExternalID:      strconv.Itoa(movieID),
				ExternalStatus:  "imported",
			}, nil
		}
	}
	evaluation := EvaluateQueue(queues)
	return StatusFromQueueEvaluation("radarr", movieID, evaluation), nil
}

func (c *RadarrClient) movieByID(ctx context.Context, client *httpclient.Client, movieID int) (movieStatusResource, error) {
	var movie movieStatusResource
	if err := client.GetJSON(ctx, "/api/v3/movie/"+strconv.Itoa(movieID), &movie); err != nil {
		return movieStatusResource{}, err
	}
	return movie, nil
}

func (c *RadarrClient) lookupMovie(ctx context.Context, client *httpclient.Client, tmdbID int) (movieResource, error) {
	values := url.Values{}
	values.Set("tmdbId", strconv.Itoa(tmdbID))
	// Radarr's /api/v3/movie/lookup/tmdb returns a single MovieResource, unlike
	// /api/v3/movie/lookup which returns an array. Missing IDs return non-2xx
	// (handled as *StatusError upstream), so a successful response is the movie.
	var movie movieResource
	if err := client.GetJSON(ctx, "/api/v3/movie/lookup/tmdb?"+values.Encode(), &movie); err != nil {
		return movieResource{}, err
	}
	if movie.TMDBID == 0 {
		movie.TMDBID = tmdbID
	}
	return movie, nil
}

func (c *RadarrClient) queueDetails(ctx context.Context, client *httpclient.Client, movieID int) ([]QueueResource, error) {
	values := url.Values{}
	values.Set("movieId", strconv.Itoa(movieID))
	var queues []QueueResource
	if err := client.GetJSON(ctx, "/api/v3/queue/details?"+values.Encode(), &queues); err != nil {
		return nil, err
	}
	return queues, nil
}

func resultFromMovie(movie movieResource) FulfillmentResult {
	externalID := ""
	if movie.ID > 0 {
		externalID = strconv.Itoa(movie.ID)
	}
	return FulfillmentResult{
		IntegrationKind: "radarr",
		ExternalID:      externalID,
		ExternalStatus:  "queued",
	}
}
