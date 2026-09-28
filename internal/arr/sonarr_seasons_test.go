package arr

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSeason is one season of a fake Sonarr series. Files counts the aired
// episodes on disk; every episode counts as aired.
type fakeSeason struct {
	Number    int
	Monitored bool
	Episodes  int
	Files     int
}

type fakeEpisode struct {
	ID        int
	Season    int
	Monitored bool
}

// fakeSonarr is a small stateful Sonarr v4 for season requests: it applies
// adds, series updates and episode monitoring the way Sonarr does, and records
// what the plugin sent.
type fakeSonarr struct {
	t  *testing.T
	mu sync.Mutex

	tvdbID    int
	seriesID  int // 0 until the series exists
	monitored bool
	seasons   []fakeSeason
	episodes  []fakeEpisode
	queue     string

	posted       map[string]any
	puts         []map[string]any
	monitorCalls [][]int
	commands     []map[string]any
}

// newFakeSonarr describes a series with the given seasons. exists controls
// whether Sonarr already has it; lookup always offers it with every season
// monitored, as Sonarr's lookup does.
func newFakeSonarr(t *testing.T, exists bool, seasons ...fakeSeason) *fakeSonarr {
	f := &fakeSonarr{t: t, tvdbID: 121361, seasons: seasons, monitored: true, queue: `[]`}
	if exists {
		f.seriesID = 24
		f.resetEpisodes()
	}
	return f
}

// resetEpisodes creates each season's episodes, monitored with their season.
func (f *fakeSonarr) resetEpisodes() {
	f.episodes = nil
	id := 100
	for _, s := range f.seasons {
		for range s.Episodes {
			id++
			f.episodes = append(f.episodes, fakeEpisode{ID: id, Season: s.Number, Monitored: s.Monitored})
		}
	}
}

// statistics mirrors Sonarr: episodeCount counts monitored episodes and
// episodes with a file.
func (f *fakeSonarr) statistics(s fakeSeason) map[string]any {
	monitored := 0
	for _, e := range f.episodes {
		if e.Season == s.Number && e.Monitored {
			monitored++
		}
	}
	count := max(monitored, s.Files)
	return map[string]any{"episodeCount": count, "episodeFileCount": s.Files}
}

func (f *fakeSonarr) seriesJSON() map[string]any {
	var seasons []any
	for _, s := range f.seasons {
		seasons = append(seasons, map[string]any{
			"seasonNumber": s.Number, "monitored": s.Monitored, "statistics": f.statistics(s),
		})
	}
	return map[string]any{
		"id": f.seriesID, "tvdbId": f.tvdbID, "title": "Game of Thrones", "monitored": f.monitored,
		"seasons": seasons,
		// Fields the plugin does not model must survive a read-modify-write.
		"path": "/tv/Game of Thrones", "qualityProfileId": 7, "monitorNewItems": "all",
	}
}

func (f *fakeSonarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	write := func(v any) {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			f.t.Errorf("encode: %v", err)
		}
	}
	decode := func() map[string]any {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Fatalf("decode %s %s: %v", r.Method, r.URL.Path, err)
		}
		return body
	}
	seriesPath := "/api/v3/series/" + strconv.Itoa(f.seriesID)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series":
		if f.seriesID == 0 {
			write([]any{})
			return
		}
		write([]any{f.seriesJSON()})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/lookup":
		var seasons []any
		for _, s := range f.seasons {
			seasons = append(seasons, map[string]any{"seasonNumber": s.Number, "monitored": s.Number > 0})
		}
		write([]any{map[string]any{"title": "Game of Thrones", "tvdbId": f.tvdbID, "titleSlug": "game-of-thrones", "seasons": seasons, "monitorNewItems": "all"}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/series":
		f.posted = decode()
		f.seriesID = 24
		monitoredSeasons := map[int]bool{}
		if seasons, ok := f.posted["seasons"].([]any); ok {
			for _, entry := range seasons {
				s := entry.(map[string]any)
				monitoredSeasons[int(s["seasonNumber"].(float64))] = s["monitored"].(bool)
			}
		}
		for i := range f.seasons {
			f.seasons[i].Monitored = monitoredSeasons[f.seasons[i].Number]
		}
		f.resetEpisodes()
		write(map[string]any{"id": f.seriesID, "tvdbId": f.tvdbID})
	case r.Method == http.MethodGet && f.seriesID != 0 && r.URL.Path == seriesPath:
		write(f.seriesJSON())
	case r.Method == http.MethodPut && f.seriesID != 0 && r.URL.Path == seriesPath:
		body := decode()
		f.puts = append(f.puts, body)
		f.monitored = body["monitored"].(bool)
		for _, entry := range body["seasons"].([]any) {
			s := entry.(map[string]any)
			n, monitored := int(s["seasonNumber"].(float64)), s["monitored"].(bool)
			for i := range f.seasons {
				if f.seasons[i].Number != n || f.seasons[i].Monitored == monitored {
					continue
				}
				// Sonarr sets every episode of a season whose flag changes.
				f.seasons[i].Monitored = monitored
				for j := range f.episodes {
					if f.episodes[j].Season == n {
						f.episodes[j].Monitored = monitored
					}
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
		write(f.seriesJSON())
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episode":
		if got := r.URL.Query().Get("seriesId"); got != strconv.Itoa(f.seriesID) {
			f.t.Fatalf("episode seriesId = %q", got)
		}
		var out []any
		for _, e := range f.episodes {
			out = append(out, map[string]any{"id": e.ID, "seasonNumber": e.Season, "monitored": e.Monitored})
		}
		write(out)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v3/episode/monitor":
		body := decode()
		var ids []int
		for _, id := range body["episodeIds"].([]any) {
			ids = append(ids, int(id.(float64)))
		}
		f.monitorCalls = append(f.monitorCalls, ids)
		for j := range f.episodes {
			if slices.Contains(ids, f.episodes[j].ID) {
				f.episodes[j].Monitored = body["monitored"].(bool)
			}
		}
		w.WriteHeader(http.StatusAccepted)
		write([]any{})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
		f.commands = append(f.commands, decode())
		w.WriteHeader(http.StatusCreated)
		write(map[string]any{"id": len(f.commands), "status": "queued"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue/details":
		_, _ = w.Write([]byte(f.queue))
	default:
		f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
	}
}

// searchedSeasons lists the seasons the plugin asked Sonarr to search.
func (f *fakeSonarr) searchedSeasons() []int {
	var out []int
	for _, c := range f.commands {
		if c["name"] != "SeasonSearch" || int(c["seriesId"].(float64)) != f.seriesID {
			f.t.Fatalf("unexpected command %v", c)
		}
		out = append(out, int(c["seasonNumber"].(float64)))
	}
	return out
}

func (f *fakeSonarr) seasonMonitored() map[int]bool {
	out := map[int]bool{}
	for _, s := range f.seasons {
		out[s.Number] = s.Monitored
	}
	return out
}

func (f *fakeSonarr) submit(t *testing.T, seasons []int, options map[string]any) FulfillmentResult {
	t.Helper()
	server := httptest.NewServer(f)
	defer server.Close()
	qualityProfileID := 3
	tvdbID := f.tvdbID
	result, err := NewSonarrClient(server.Client()).SubmitSeries(context.Background(), Request{
		MediaType: MediaTypeSeries,
		TVDBID:    &tvdbID,
		Title:     "Game of Thrones",
		Seasons:   seasons,
	}, Instance{
		Kind:             "sonarr",
		BaseURL:          server.URL,
		APIKeyRef:        "sonarr-key",
		RootFolder:       "/series",
		QualityProfileID: &qualityProfileID,
		Options:          options,
	})
	if err != nil {
		t.Fatalf("SubmitSeries returned error: %v", err)
	}
	if result.ExternalID != "24" {
		t.Fatalf("result = %+v, want Sonarr series 24", result)
	}
	return result
}

func fourSeasons() []fakeSeason {
	return []fakeSeason{
		{Number: 0, Episodes: 2},
		{Number: 1, Episodes: 10},
		{Number: 2, Episodes: 10},
		{Number: 3, Episodes: 10},
	}
}

// A new series requested for some seasons is added with only those seasons
// monitored, whatever the connection's monitor policy, and Sonarr's add-time
// search then covers just them.
func TestSubmitSeriesWithSeasonsMonitorsOnlyRequestedSeasons(t *testing.T) {
	f := newFakeSonarr(t, false, fourSeasons()...)
	f.submit(t, []int{0, 2}, map[string]any{"monitor": "all", "search_on_add": true})

	var seasons []map[string]any
	for _, entry := range f.posted["seasons"].([]any) {
		seasons = append(seasons, entry.(map[string]any))
	}
	want := map[int]bool{0: true, 1: false, 2: true, 3: false}
	if len(seasons) != len(want) {
		t.Fatalf("posted seasons = %v, want all four", seasons)
	}
	for _, s := range seasons {
		n := int(s["seasonNumber"].(float64))
		if s["monitored"] != want[n] {
			t.Errorf("season %d monitored = %v, want %v", n, s["monitored"], want[n])
		}
		if _, ok := s["statistics"]; ok {
			t.Errorf("season %d carries statistics in the add payload", n)
		}
	}
	addOptions := f.posted["addOptions"].(map[string]any)
	if _, ok := addOptions["monitor"]; ok {
		t.Fatalf("addOptions.monitor = %v; a season request must leave it unset so Sonarr keeps the season flags", addOptions["monitor"])
	}
	if addOptions["searchForMissingEpisodes"] != true {
		t.Fatalf("addOptions.searchForMissingEpisodes = %v, want true", addOptions["searchForMissingEpisodes"])
	}
	if f.posted["monitorNewItems"] != "none" || f.posted["monitored"] != true {
		t.Fatalf("monitorNewItems = %v, monitored = %v; want none, true", f.posted["monitorNewItems"], f.posted["monitored"])
	}
	if len(f.commands) != 0 {
		t.Fatalf("commands = %v; the add searches, no separate search wanted", f.commands)
	}
}

// Adding seasons to a series Sonarr already has monitors them, episodes
// included, searches them, and leaves the other seasons alone.
func TestSubmitSeriesWithSeasonsExtendsExistingSeries(t *testing.T) {
	f := newFakeSonarr(t, true,
		fakeSeason{Number: 0, Episodes: 2},
		fakeSeason{Number: 1, Monitored: true, Episodes: 10, Files: 10},
		fakeSeason{Number: 2, Episodes: 10},
		fakeSeason{Number: 3, Monitored: true, Episodes: 10, Files: 2},
		fakeSeason{Number: 4, Monitored: true, Episodes: 10},
	)
	// A "future" policy left season 3's aired episodes unmonitored.
	for i := range f.episodes {
		if f.episodes[i].Season == 3 && i%2 == 0 {
			f.episodes[i].Monitored = false
		}
	}
	f.submit(t, []int{2, 3}, map[string]any{"search_on_add": true})

	if len(f.puts) != 1 {
		t.Fatalf("series updates = %d, want 1", len(f.puts))
	}
	put := f.puts[0]
	if put["path"] != "/tv/Game of Thrones" || put["qualityProfileId"] != float64(7) || put["monitorNewItems"] != "all" {
		t.Fatalf("series update dropped fields it does not own: %v", put)
	}
	want := map[int]bool{0: false, 1: true, 2: true, 3: true, 4: true}
	if got := f.seasonMonitored(); !maps.Equal(got, want) {
		t.Fatalf("season monitored = %v, want %v", got, want)
	}
	for _, e := range f.episodes {
		if (e.Season == 2 || e.Season == 3) && !e.Monitored {
			t.Fatalf("episode %d of season %d left unmonitored", e.ID, e.Season)
		}
	}
	if len(f.monitorCalls) != 1 || len(f.monitorCalls[0]) != 5 {
		t.Fatalf("episode monitor calls = %v, want season 3's five unmonitored episodes", f.monitorCalls)
	}
	if got := f.searchedSeasons(); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("searched seasons = %v, want [2 3]", got)
	}
}

// Repeating a season request changes nothing more and searches only a season
// that is still missing episodes.
func TestSubmitSeriesWithSeasonsConvergesOnRepeat(t *testing.T) {
	f := newFakeSonarr(t, false, fourSeasons()...)
	f.submit(t, []int{1, 2}, map[string]any{"search_on_add": true})
	if f.posted == nil {
		t.Fatal("first request did not add the series")
	}
	f.seasons[1].Files = 10 // season 1 finished downloading
	f.posted = nil

	f.submit(t, []int{1, 2}, map[string]any{"search_on_add": true})
	if f.posted != nil || len(f.puts) != 0 || len(f.monitorCalls) != 0 {
		t.Fatalf("repeat changed the series: posted=%v puts=%d monitor calls=%v", f.posted, len(f.puts), f.monitorCalls)
	}
	if got := f.searchedSeasons(); !slices.Equal(got, []int{2}) {
		t.Fatalf("searched seasons = %v, want only the incomplete season 2", got)
	}
	if got, want := f.seasonMonitored(), map[int]bool{0: false, 1: true, 2: true, 3: false}; !maps.Equal(got, want) {
		t.Fatalf("season monitored = %v, want %v", got, want)
	}
}

// With search on add off, seasons are monitored but not searched.
func TestSubmitSeriesWithSeasonsHonoursSearchOnAdd(t *testing.T) {
	f := newFakeSonarr(t, true, fakeSeason{Number: 1, Monitored: true, Episodes: 5}, fakeSeason{Number: 2, Episodes: 5})
	f.submit(t, []int{2}, map[string]any{"search_on_add": false})
	if !f.seasonMonitored()[2] {
		t.Fatal("season 2 not monitored")
	}
	if len(f.commands) != 0 {
		t.Fatalf("commands = %v, want none with search on add off", f.commands)
	}
}

// A whole-series request keeps today's behaviour: a new series follows the
// monitor policy and sends no season flags, and an existing one is adopted
// untouched.
func TestSubmitSeriesWholeSeriesUnchanged(t *testing.T) {
	f := newFakeSonarr(t, false, fourSeasons()...)
	f.submit(t, nil, map[string]any{"monitor": "lastSeason"})
	addOptions := f.posted["addOptions"].(map[string]any)
	if addOptions["monitor"] != "lastSeason" {
		t.Fatalf("addOptions.monitor = %v, want lastSeason", addOptions["monitor"])
	}
	if _, ok := f.posted["seasons"]; ok {
		t.Fatalf("whole-series add sent seasons %v", f.posted["seasons"])
	}
	if _, ok := f.posted["monitorNewItems"]; ok {
		t.Fatalf("whole-series add sent monitorNewItems %v", f.posted["monitorNewItems"])
	}

	existing := newFakeSonarr(t, true, fourSeasons()...)
	existing.submit(t, nil, nil)
	if len(existing.puts) != 0 || len(existing.commands) != 0 || len(existing.monitorCalls) != 0 {
		t.Fatalf("whole-series adoption changed the series: puts=%d commands=%v", len(existing.puts), existing.commands)
	}
}

// A request for seasons Sonarr does not know yet fails instead of adding a
// series that would acquire nothing.
func TestSubmitSeriesWithUnknownSeasonsFails(t *testing.T) {
	for _, exists := range []bool{false, true} {
		f := newFakeSonarr(t, exists, fakeSeason{Number: 1, Episodes: 5})
		server := httptest.NewServer(f)
		qualityProfileID := 3
		tvdbID := f.tvdbID
		_, err := NewSonarrClient(server.Client()).SubmitSeries(context.Background(), Request{
			MediaType: MediaTypeSeries, TVDBID: &tvdbID, Seasons: []int{5},
		}, Instance{Kind: "sonarr", BaseURL: server.URL, APIKeyRef: "k", RootFolder: "/series", QualityProfileID: &qualityProfileID})
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "none of the requested seasons (5)") {
			t.Fatalf("exists=%v: error = %v, want none of the requested seasons", exists, err)
		}
		if f.posted != nil || len(f.puts) != 0 {
			t.Fatalf("exists=%v: series changed despite the error", exists)
		}
	}
}

func checkSeasons(t *testing.T, f *fakeSonarr, seasons []int) FulfillmentStatus {
	t.Helper()
	server := httptest.NewServer(f)
	defer server.Close()
	status, err := NewSonarrClient(server.Client()).CheckSeriesStatus(context.Background(), Request{
		MediaType: MediaTypeSeries, ExternalID: "24", Seasons: seasons,
	}, Instance{Kind: "sonarr", BaseURL: server.URL, APIKeyRef: "sonarr-key"})
	if err != nil {
		t.Fatalf("CheckSeriesStatus: %v", err)
	}
	return status
}

// Completion of a season request looks only at its seasons: other monitored
// seasons still downloading do not hold it back, and its own missing episodes
// do.
func TestCheckSeriesStatusScopesCompletionToRequestedSeasons(t *testing.T) {
	f := newFakeSonarr(t, true,
		fakeSeason{Number: 1, Monitored: true, Episodes: 10, Files: 3},
		fakeSeason{Number: 2, Monitored: true, Episodes: 10, Files: 10},
	)
	if got := checkSeasons(t, f, []int{2}); got.Status != StatusCompleted {
		t.Fatalf("season 2 request status = %+v, want completed", got)
	}
	if got := checkSeasons(t, f, []int{1, 2}); got.Status != StatusQueued {
		t.Fatalf("seasons 1-2 request status = %+v, want queued", got)
	}
	if got := checkSeasons(t, f, nil); got.Status != StatusQueued {
		t.Fatalf("whole-series request status = %+v, want queued", got)
	}
}

// Queue entries for other seasons are not the request's downloads.
func TestCheckSeriesStatusIgnoresOtherSeasonsInQueue(t *testing.T) {
	f := newFakeSonarr(t, true,
		fakeSeason{Number: 1, Monitored: true, Episodes: 10},
		fakeSeason{Number: 2, Monitored: true, Episodes: 10, Files: 10},
	)
	f.queue = `[{"seriesId":24,"seasonNumber":1,"status":"failed","title":"S01 pack"}]`
	if got := checkSeasons(t, f, []int{2}); got.Status != StatusCompleted {
		t.Fatalf("season 2 request status = %+v, want completed despite season 1's failed download", got)
	}
	if got := checkSeasons(t, f, []int{1}); got.Outcome != OutcomeFailed {
		t.Fatalf("season 1 request status = %+v, want a failed outcome", got)
	}
	if got := checkSeasons(t, f, nil); got.Outcome != OutcomeFailed {
		t.Fatalf("whole-series request status = %+v, want a failed outcome", got)
	}
}

// Progress covers only the request's seasons, and a season pack that Sonarr
// lists under each of its episodes counts once.
func TestCheckSeriesStatusReportsProgressForRequestedSeasons(t *testing.T) {
	f := newFakeSonarr(t, true,
		fakeSeason{Number: 1, Monitored: true, Episodes: 3},
		fakeSeason{Number: 2, Monitored: true, Episodes: 10},
		fakeSeason{Number: 3, Monitored: true, Episodes: 10, Files: 10},
	)
	f.queue = `[
	  {"id":1,"seriesId":24,"seasonNumber":1,"status":"downloading","trackedDownloadState":"downloading","downloadId":"s01","size":3000,"sizeleft":1200,"estimatedCompletionTime":"2026-09-28T12:30:00Z"},
	  {"id":2,"seriesId":24,"seasonNumber":1,"status":"downloading","trackedDownloadState":"downloading","downloadId":"s01","size":3000,"sizeleft":1200,"estimatedCompletionTime":"2026-09-28T12:30:00Z"},
	  {"id":3,"seriesId":24,"seasonNumber":1,"status":"downloading","trackedDownloadState":"downloading","downloadId":"s01","size":3000,"sizeleft":1200,"estimatedCompletionTime":"2026-09-28T12:30:00Z"},
	  {"id":4,"seriesId":24,"seasonNumber":2,"status":"paused","trackedDownloadState":"downloading","downloadId":"s02e01","size":500,"sizeleft":500,"estimatedCompletionTime":"2026-09-28T14:00:00Z"}
	]`
	cases := []struct {
		name    string
		seasons []int
		want    *Progress
		eta     string
	}{
		{"season 1", []int{1}, &Progress{Phase: PhaseDownloading, BytesTotal: 3000, BytesLeft: 1200, Downloads: 1}, "2026-09-28T12:30:00Z"},
		{"season 2", []int{2}, &Progress{Phase: PhasePaused, BytesTotal: 500, BytesLeft: 500, Downloads: 1}, "2026-09-28T14:00:00Z"},
		{"whole series", nil, &Progress{Phase: PhaseDownloading, BytesTotal: 3500, BytesLeft: 1700, Downloads: 2}, "2026-09-28T14:00:00Z"},
		{"completed season", []int{3}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSeasons(t, f, tc.seasons).Progress
			if tc.want == nil {
				if got != nil {
					t.Fatalf("progress = %+v, want none", got)
				}
				return
			}
			if got == nil || got.Phase != tc.want.Phase || got.BytesTotal != tc.want.BytesTotal ||
				got.BytesLeft != tc.want.BytesLeft || got.Downloads != tc.want.Downloads {
				t.Fatalf("progress = %+v, want %+v", got, tc.want)
			}
			eta, err := time.Parse(time.RFC3339, tc.eta)
			if err != nil {
				t.Fatal(err)
			}
			if got.EstimatedCompletion == nil || !got.EstimatedCompletion.Equal(eta) {
				t.Fatalf("estimated completion = %v, want %v", got.EstimatedCompletion, eta)
			}
		})
	}
}
