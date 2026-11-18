package arr

import (
	"cmp"
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

// Season requests name seasons as the host numbers them (TMDB), and Sonarr
// numbers some series differently: TMDB's one 24-episode season of DAN DA
// DAN is Sonarr's seasons 1 and 2. When the host sends a season's first air
// date and episode count, the plugin finds the same episodes on Sonarr by
// air date and works on the Sonarr seasons that hold them. A season without
// a date or count, or whose episodes Sonarr does not list, is matched by
// number, as before.

const (
	defaultAddPoll = time.Second
	// defaultAddWait bounds the wait for Sonarr to finish adding a series,
	// usually well under a second. It stays well inside the host's 60s
	// request_router call limit, which covers every connection of a request.
	defaultAddWait = 20 * time.Second
)

// airDateTolerance lets an episode dated a day before the host's first air
// date still count: TMDB and Sonarr can date a late-night premiere in
// different time zones.
const airDateTolerance = 24 * time.Hour

// ErrStillAdding reports that Sonarr had not finished adding a series within
// the wait. Fulfill answers it as a temporary failure so the host retries the
// submission, which then adopts the series.
var ErrStillAdding = errors.New("sonarr: still adding the series; its seasons are matched on retry")

// datedSeasons reports whether a season request has a season it can match
// by air date.
func datedSeasons(req Request) bool {
	if len(req.Seasons) == 0 {
		return false
	}
	for _, d := range req.SeasonDetails {
		if _, ok := firstAired(d); ok {
			return true
		}
	}
	return false
}

// firstAired is the season's first air date, when it can be matched by air
// date.
func firstAired(d SeasonDetail) (time.Time, bool) {
	if d.Number <= 0 || d.EpisodeCount <= 0 {
		return time.Time{}, false
	}
	first, err := time.Parse(time.DateOnly, d.FirstAirDate)
	return first, err == nil
}

// laterSeasonEpisodes is how many of a requested season's episodes a later
// Sonarr season must hold to count: one or two are TMDB and Sonarr
// disagreeing on a count or a date, not a split.
const laterSeasonEpisodes = 3

// matchSeasons maps the requested seasons to the Sonarr seasons holding the
// same episodes. A requested season is the first EpisodeCount regular
// episodes, in airing order, that air on or after its first air date. The
// Sonarr season holding the first of them always counts; a later one when it
// holds at least laterSeasonEpisodes of them.
func matchSeasons(episodes []episodeResource, req Request) []int {
	var aired []episodeResource
	for _, e := range episodes {
		if e.SeasonNumber > 0 && e.AirDate != "" {
			aired = append(aired, e)
		}
	}
	slices.SortFunc(aired, func(a, b episodeResource) int {
		return cmp.Or(cmp.Compare(a.AirDate, b.AirDate), cmp.Compare(a.SeasonNumber, b.SeasonNumber),
			cmp.Compare(a.EpisodeNumber, b.EpisodeNumber))
	})
	var out []int
	for _, number := range req.Seasons {
		i := slices.IndexFunc(req.SeasonDetails, func(d SeasonDetail) bool { return d.Number == number })
		var first time.Time
		ok := i >= 0
		if ok {
			first, ok = firstAired(req.SeasonDetails[i])
		}
		if !ok {
			out = append(out, number)
			continue
		}
		d := req.SeasonDetails[i]
		from := first.Add(-airDateTolerance).Format(time.DateOnly)
		matched := map[int]int{}
		firstSeason, total := 0, 0
		for _, e := range aired {
			if total == d.EpisodeCount {
				break
			}
			if e.AirDate < from {
				continue
			}
			if total == 0 {
				firstSeason = e.SeasonNumber
			}
			matched[e.SeasonNumber]++
			total++
		}
		if total == 0 {
			out = append(out, number)
			continue
		}
		for season, n := range matched {
			if season == firstSeason || n >= laterSeasonEpisodes {
				out = append(out, season)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sonarrSeasons returns the request's seasons in Sonarr's numbering for a
// series with the given seasons. It reads the episodes only when a season
// can be matched by air date.
func (c *SonarrClient) sonarrSeasons(ctx context.Context, client *httpclient.Client, seriesID int, seasons []seasonResource, req Request) ([]int, error) {
	if !datedSeasons(req) {
		return req.Seasons, nil
	}
	episodes, err := regularEpisodes(ctx, client, seriesID, seasons)
	if err != nil {
		return nil, err
	}
	return matchSeasons(episodes, req), nil
}

// seasonEpisodes reads one season's episodes. Reads go one season at a time:
// the whole episode list of a series with 1,000+ episodes is too large to
// read in one response.
func seasonEpisodes(ctx context.Context, client *httpclient.Client, seriesID, seasonNumber int) ([]episodeResource, error) {
	values := url.Values{}
	values.Set("seriesId", strconv.Itoa(seriesID))
	values.Set("seasonNumber", strconv.Itoa(seasonNumber))
	var episodes []episodeResource
	if err := client.GetJSON(ctx, "/api/v3/episode?"+values.Encode(), &episodes); err != nil {
		return nil, err
	}
	return episodes, nil
}

// regularEpisodes reads the episodes of the series' regular seasons.
func regularEpisodes(ctx context.Context, client *httpclient.Client, seriesID int, seasons []seasonResource) ([]episodeResource, error) {
	var out []episodeResource
	for _, season := range seasons {
		if season.SeasonNumber <= 0 {
			continue
		}
		episodes, err := seasonEpisodes(ctx, client, seriesID, season.SeasonNumber)
		if err != nil {
			return nil, err
		}
		out = append(out, episodes...)
	}
	return out, nil
}

// waitUntilAdded waits for Sonarr to finish adding a new series and returns
// its seasons. Sonarr lists the episodes before it has finished: matching
// then could see a partly written list, and its post-add handling writes back
// the season flags it read first, undoing monitoring set meanwhile. The
// series carries addOptions until then. It answers ErrStillAdding when the
// wait runs out.
func (c *SonarrClient) waitUntilAdded(ctx context.Context, client *httpclient.Client, seriesID int) ([]seasonResource, error) {
	waitCtx, cancel := context.WithTimeout(ctx, cmp.Or(c.addWait, defaultAddWait))
	defer cancel()
	poll := cmp.Or(c.addPoll, defaultAddPoll)
	for {
		series, err := c.seriesByID(waitCtx, client, seriesID)
		if err == nil && series.AddOptions == nil {
			return series.Seasons, nil
		}
		if err != nil && waitCtx.Err() == nil {
			return nil, err
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrStillAdding
		case <-time.After(poll):
		}
	}
}
