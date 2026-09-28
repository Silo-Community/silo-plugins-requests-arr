package arr

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvaluateQueueFailureWinsOverDownloading(t *testing.T) {
	result := EvaluateQueue([]QueueResource{
		{Status: "downloading", TrackedDownloadState: "downloading"},
		{Title: "Bad Release", Status: "queued", TrackedDownloadState: "failedPending"},
	})

	if result.State != QueueStateFailed {
		t.Fatalf("state = %q, want failed", result.State)
	}
	if result.ExternalStatus != "queued/failedPending" {
		t.Fatalf("external status = %q, want queued/failedPending", result.ExternalStatus)
	}
	if result.Message == "" {
		t.Fatal("message should describe the queue failure")
	}
}

// A download the arr has finished but not imported is still in flight, whether
// the import is running, waiting, or blocked. Evaluating it as queued would
// take the target off the host's one-minute pass while its progress says the
// import needs attention.
func TestEvaluateQueueCountsImportsAsDownloading(t *testing.T) {
	cases := []struct {
		name          string
		status        string
		trackedStatus string
		state         string
	}{
		{"import blocked", "completed", "warning", "importBlocked"},
		{"import blocked without a warning", "completed", "ok", "importBlocked"},
		{"import pending with a warning", "completed", "warning", "importPending"},
		{"import pending", "completed", "ok", "importPending"},
		{"importing", "completed", "ok", "importing"},
		{"downloaded, import not possible", "completed", "warning", "downloading"},
		{"downloaded, not yet taken up for import", "completed", "ok", "downloading"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := QueueResource{
				Status:                tc.status,
				TrackedDownloadStatus: tc.trackedStatus,
				TrackedDownloadState:  tc.state,
			}
			if phase := queuePhase(item); phase != PhaseImportBlocked && phase != PhaseImporting {
				t.Fatalf("queuePhase = %q, want an import phase", phase)
			}
			got := EvaluateQueue([]QueueResource{item})
			want := tc.status + "/" + tc.state
			if got.State != QueueStateDownloading || got.ExternalStatus != want {
				t.Fatalf("evaluation = %+v, want downloading %q", got, want)
			}
		})
	}
}

func TestQueuePhase(t *testing.T) {
	cases := []struct {
		name          string
		status        string
		trackedStatus string
		state         string
		want          string
	}{
		{"import blocked", "completed", "warning", "importBlocked", PhaseImportBlocked},
		{"import pending with a warning", "completed", "warning", "importPending", PhaseImportBlocked},
		{"import pending with an error", "completed", "error", "importPending", PhaseImportBlocked},
		{"import pending", "completed", "ok", "importPending", PhaseImporting},
		{"importing", "completed", "ok", "importing", PhaseImporting},
		// Radarr and Sonarr keep a finished download in the downloading state,
		// with a warning, while they cannot import it.
		{"downloaded, import not possible", "completed", "warning", "downloading", PhaseImportBlocked},
		{"downloaded, not yet taken up for import", "completed", "ok", "downloading", PhaseImporting},
		{"paused", "paused", "ok", "downloading", PhasePaused},
		{"paused with a warning", "paused", "warning", "downloading", PhasePaused},
		{"client warning", "warning", "warning", "downloading", PhaseStalled},
		{"downloading with a warning", "downloading", "warning", "downloading", PhaseStalled},
		{"downloading with an error", "downloading", "error", "downloading", PhaseStalled},
		{"downloading", "downloading", "ok", "downloading", PhaseDownloading},
		{"queued in the client", "queued", "ok", "downloading", PhaseQueued},
		{"delayed", "delay", "", "", PhaseQueued},
		{"client unavailable", "downloadClientUnavailable", "warning", "downloading", PhaseQueued},
		{"fallback", "fallback", "", "", PhaseQueued},
		{"unknown", "unknown", "", "", PhaseQueued},
		{"nothing reported", "", "", "", PhaseQueued},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := queuePhase(QueueResource{
				Status:                tc.status,
				TrackedDownloadStatus: tc.trackedStatus,
				TrackedDownloadState:  tc.state,
			})
			if got != tc.want {
				t.Fatalf("queuePhase = %q, want %q", got, tc.want)
			}
		})
	}
}

// phaseItem is a queue item that maps to the given phase.
func phaseItem(phase, downloadID string) QueueResource {
	item := QueueResource{DownloadID: downloadID}
	switch phase {
	case PhaseImportBlocked:
		item.Status, item.TrackedDownloadState = "completed", "importBlocked"
	case PhaseStalled:
		item.Status, item.TrackedDownloadState = "warning", "downloading"
	case PhaseDownloading:
		item.Status, item.TrackedDownloadState = "downloading", "downloading"
	case PhaseImporting:
		item.Status, item.TrackedDownloadState = "completed", "importing"
	case PhasePaused:
		item.Status, item.TrackedDownloadState = "paused", "downloading"
	default:
		item.Status = "queued"
	}
	return item
}

func TestEvaluateProgressPhasePrecedence(t *testing.T) {
	cases := []struct {
		name   string
		phases []string
		want   string
	}{
		{"one download", []string{PhaseDownloading}, PhaseDownloading},
		{"paused over queued", []string{PhaseQueued, PhasePaused}, PhasePaused},
		{"importing over paused", []string{PhasePaused, PhaseImporting}, PhaseImporting},
		{"downloading over importing", []string{PhaseImporting, PhaseDownloading}, PhaseDownloading},
		{"stalled over downloading", []string{PhaseDownloading, PhaseStalled}, PhaseStalled},
		{"import blocked over stalled", []string{PhaseStalled, PhaseImportBlocked}, PhaseImportBlocked},
		{"import blocked over everything", []string{PhaseQueued, PhaseImportBlocked, PhaseDownloading, PhasePaused}, PhaseImportBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]QueueResource, 0, len(tc.phases))
			for i, phase := range tc.phases {
				items = append(items, phaseItem(phase, string(rune('a'+i))))
			}
			got := EvaluateProgress(items)
			if got == nil || got.Phase != tc.want {
				t.Fatalf("progress = %+v, want phase %q", got, tc.want)
			}
			if got.Downloads != len(tc.phases) {
				t.Fatalf("downloads = %d, want %d", got.Downloads, len(tc.phases))
			}
		})
	}
}

func TestEvaluateProgressEmptyQueue(t *testing.T) {
	if got := EvaluateProgress(nil); got != nil {
		t.Fatalf("progress = %+v, want nil", got)
	}
}

func TestEvaluateProgressCountsEachDownloadOnce(t *testing.T) {
	cases := []struct {
		name      string
		items     []QueueResource
		total     int64
		left      int64
		downloads int
	}{
		{
			name: "season pack listed per episode",
			items: []QueueResource{
				{ID: 1, DownloadID: "pack", Size: 3000, SizeLeft: 1000},
				{ID: 2, DownloadID: "pack", Size: 3000, SizeLeft: 1000},
				{ID: 3, DownloadID: "pack", Size: 3000, SizeLeft: 1000},
				{ID: 4, DownloadID: "episode", Size: 500, SizeLeft: 500},
			},
			total: 3500, left: 1500, downloads: 2,
		},
		{
			name: "queue item id without a downloadId",
			items: []QueueResource{
				{ID: 7, Size: 800, SizeLeft: 800},
				{ID: 7, Size: 800, SizeLeft: 800},
				{ID: 8, Size: 200, SizeLeft: 100},
			},
			total: 1000, left: 900, downloads: 2,
		},
		{
			name: "items without any id",
			items: []QueueResource{
				{Size: 100, SizeLeft: 50},
				{Size: 100, SizeLeft: 50},
			},
			total: 200, left: 100, downloads: 2,
		},
		{
			// Sonarr lists a release it holds back once per episode, each item
			// with its own id, the release's title and size, and no downloadId.
			name: "held-back season pack listed per episode",
			items: []QueueResource{
				{ID: 1, DownloadID: "s01", Title: "Show.S01.1080p", Status: "downloading", Size: 1000, SizeLeft: 500},
				{ID: 2, DownloadID: "s01", Title: "Show.S01.1080p", Status: "downloading", Size: 1000, SizeLeft: 500},
				{ID: 1482910337, Title: "Show.S02.1080p", Status: "delay", Size: 5000, SizeLeft: 5000},
				{ID: 902113458, Title: "Show.S02.1080p", Status: "delay", Size: 5000, SizeLeft: 5000},
				{ID: 1710254901, Title: "Show.S02.1080p", Status: "delay", Size: 5000, SizeLeft: 5000},
				{ID: 55512093, Title: "Show.S02.1080p", Status: "delay", Size: 5000, SizeLeft: 5000},
			},
			total: 6000, left: 5500, downloads: 2,
		},
		{
			name: "held-back releases that differ",
			items: []QueueResource{
				{ID: 11, Title: "Show.S02.1080p", Status: "delay", Size: 5000, SizeLeft: 5000},
				{ID: 12, Title: "Show.S03.1080p", Status: "delay", Size: 5000, SizeLeft: 5000},
				{ID: 13, Title: "Show.S03.1080p", Status: "delay", Size: 6000, SizeLeft: 6000},
			},
			total: 16000, left: 16000, downloads: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateProgress(tc.items)
			if got.BytesTotal != tc.total || got.BytesLeft != tc.left || got.Downloads != tc.downloads {
				t.Fatalf("progress = %+v, want total %d, left %d, downloads %d", got, tc.total, tc.left, tc.downloads)
			}
		})
	}
}

func TestEvaluateProgressSizes(t *testing.T) {
	cases := []struct {
		name  string
		items []QueueResource
		total int64
		left  int64
	}{
		{"decimals round", []QueueResource{{DownloadID: "a", Size: 1000.6, SizeLeft: 250.4}}, 1001, 250},
		{"size unknown", []QueueResource{{DownloadID: "a"}}, 0, 0},
		{"size unknown with bytes left", []QueueResource{{DownloadID: "a", SizeLeft: 400}}, 0, 0},
		{"left above size", []QueueResource{{DownloadID: "a", Size: 1000, SizeLeft: 1200}}, 1000, 1000},
		{"negative left", []QueueResource{{DownloadID: "a", Size: 1000, SizeLeft: -5}}, 1000, 0},
		{"negative size", []QueueResource{{DownloadID: "a", Size: -1000, SizeLeft: 400}}, 0, 0},
		{"size rounds to zero", []QueueResource{{DownloadID: "a", Size: 0.4, SizeLeft: 0.4}}, 0, 0},
		// Summing only the known sizes would show 60% for a target that may
		// be far from done, so no download's size is reported.
		{"one size unknown", []QueueResource{{DownloadID: "a", Size: 1000, SizeLeft: 400}, {DownloadID: "b"}}, 0, 0},
		{"one size unknown, listed last", []QueueResource{{DownloadID: "a", Size: 1000, SizeLeft: 400}, {DownloadID: "b", Size: 500, SizeLeft: 0}, {DownloadID: "c", SizeLeft: 300}}, 0, 0},
		{"one size unknown, listed first", []QueueResource{{DownloadID: "c"}, {DownloadID: "a", Size: 1000, SizeLeft: 400}}, 0, 0},
		{"season pack listed per episode, all sizes known", []QueueResource{{DownloadID: "pack", Size: 3000, SizeLeft: 1000}, {DownloadID: "pack", Size: 3000, SizeLeft: 1000}, {DownloadID: "b", Size: 500, SizeLeft: 500}}, 3500, 1500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateProgress(tc.items)
			if got.BytesTotal != tc.total || got.BytesLeft != tc.left {
				t.Fatalf("progress = %+v, want total %d, left %d", got, tc.total, tc.left)
			}
		})
	}
}

// A download without a known size zeroes the byte counts and nothing else.
func TestEvaluateProgressUnknownSizeKeepsPhaseAndEstimate(t *testing.T) {
	eta := time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)
	got := EvaluateProgress([]QueueResource{
		{DownloadID: "a", Status: "downloading", TrackedDownloadState: "downloading", Size: 1000, SizeLeft: 400, EstimatedCompletionTime: &eta},
		{DownloadID: "a", Status: "downloading", TrackedDownloadState: "downloading", Size: 1000, SizeLeft: 400, EstimatedCompletionTime: &eta},
		{DownloadID: "b", Status: "queued", TrackedDownloadState: "downloading"},
	})
	if got.BytesTotal != 0 || got.BytesLeft != 0 {
		t.Fatalf("bytes = %d/%d left, want 0/0", got.BytesLeft, got.BytesTotal)
	}
	if got.Phase != PhaseDownloading || got.Downloads != 2 {
		t.Fatalf("progress = %+v, want downloading with 2 downloads", got)
	}
	if got.EstimatedCompletion == nil || !got.EstimatedCompletion.Equal(eta) {
		t.Fatalf("estimated completion = %v, want %v", got.EstimatedCompletion, eta)
	}
}

func TestEvaluateProgressEstimatedCompletion(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time {
		v := now.Add(d)
		return &v
	}
	cases := []struct {
		name string
		etas []*time.Time
		want *time.Time
	}{
		{"absent", []*time.Time{nil}, nil},
		{"all absent", []*time.Time{nil, nil}, nil},
		{"latest wins", []*time.Time{at(10 * time.Minute), at(time.Hour), at(30 * time.Minute)}, at(time.Hour)},
		{"absent does not hide an estimate", []*time.Time{nil, at(5 * time.Minute)}, at(5 * time.Minute)},
		{"past estimates pass through", []*time.Time{at(-2 * time.Hour), at(-time.Hour)}, at(-time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]QueueResource, 0, len(tc.etas))
			for i, eta := range tc.etas {
				items = append(items, QueueResource{DownloadID: string(rune('a' + i)), EstimatedCompletionTime: eta})
			}
			got := EvaluateProgress(items).EstimatedCompletion
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("estimated completion = %v, want none", *got)
			case tc.want != nil && (got == nil || !got.Equal(*tc.want)):
				t.Fatalf("estimated completion = %v, want %v", got, *tc.want)
			}
		})
	}
}

// The queue as Sonarr serves it: decimal sizes, a UTC timestamp or null for
// the estimate, and one entry per episode of a grabbed season pack and of a
// held-back release. Sonarr v5 renames sizeleft to sizeLeft.
func TestEvaluateProgressDecodesQueueDetails(t *testing.T) {
	for _, sizeLeft := range []string{"sizeleft", "sizeLeft"} {
		t.Run(sizeLeft, func(t *testing.T) {
			body := `[
			  {"id":101,"seriesId":24,"episodeId":1,"seasonNumber":1,"size":4294967296.0,"` + sizeLeft + `":1073741824.5,
			   "estimatedCompletionTime":"2026-09-28T12:10:00Z","status":"downloading","trackedDownloadStatus":"ok",
			   "trackedDownloadState":"downloading","downloadId":"SABnzbd_nzo_abc"},
			  {"id":102,"seriesId":24,"episodeId":2,"seasonNumber":1,"size":4294967296.0,"` + sizeLeft + `":1073741824.5,
			   "estimatedCompletionTime":"2026-09-28T12:10:00Z","status":"downloading","trackedDownloadStatus":"ok",
			   "trackedDownloadState":"downloading","downloadId":"SABnzbd_nzo_abc"},
			  {"id":1482910337,"seriesId":24,"episodeId":3,"seasonNumber":1,"title":"Show.S01E03E04.1080p","size":734003200,
			   "` + sizeLeft + `":734003200,"estimatedCompletionTime":null,"status":"delay"},
			  {"id":902113458,"seriesId":24,"episodeId":4,"seasonNumber":1,"title":"Show.S01E03E04.1080p","size":734003200,
			   "` + sizeLeft + `":734003200,"estimatedCompletionTime":null,"status":"delay"}
			]`
			var items []QueueResource
			if err := json.Unmarshal([]byte(body), &items); err != nil {
				t.Fatalf("decode: %v", err)
			}
			got := EvaluateProgress(items)
			want := time.Date(2026, 9, 28, 12, 10, 0, 0, time.UTC)
			if got.Phase != PhaseDownloading || got.Downloads != 2 {
				t.Fatalf("progress = %+v, want downloading with 2 downloads", got)
			}
			if got.BytesTotal != 4294967296+734003200 || got.BytesLeft != 1073741825+734003200 {
				t.Fatalf("bytes = %d/%d left, want %d/%d", got.BytesLeft, got.BytesTotal, 1073741825+734003200, 4294967296+734003200)
			}
			if got.EstimatedCompletion == nil || !got.EstimatedCompletion.Equal(want) {
				t.Fatalf("estimated completion = %v, want %v", got.EstimatedCompletion, want)
			}
		})
	}
}

func TestStatusFromQueueAttachesProgressWhileLive(t *testing.T) {
	cases := []struct {
		name     string
		items    []QueueResource
		status   Status
		outcome  Outcome
		progress bool
	}{
		{"queued", []QueueResource{{DownloadID: "a", Status: "queued", Size: 10, SizeLeft: 10}}, StatusQueued, "", true},
		{"downloading", []QueueResource{{DownloadID: "a", Status: "downloading", TrackedDownloadState: "downloading", Size: 10, SizeLeft: 4}}, StatusDownloading, "", true},
		{"import blocked", []QueueResource{{DownloadID: "a", Status: "completed", TrackedDownloadStatus: "warning", TrackedDownloadState: "importBlocked", Size: 10}}, StatusDownloading, "", true},
		{"import pending", []QueueResource{{DownloadID: "a", Status: "completed", TrackedDownloadStatus: "ok", TrackedDownloadState: "importPending", Size: 10}}, StatusDownloading, "", true},
		{"failed", []QueueResource{
			{DownloadID: "a", Status: "downloading", TrackedDownloadState: "downloading", Size: 10, SizeLeft: 4},
			{DownloadID: "b", Status: "failed", Size: 10, SizeLeft: 10},
		}, StatusQueued, OutcomeFailed, false},
		{"empty queue", nil, StatusQueued, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusFromQueue("radarr", 42, tc.items)
			if got.Status != tc.status || got.Outcome != tc.outcome {
				t.Fatalf("status = %q/%q, want %q/%q", got.Status, got.Outcome, tc.status, tc.outcome)
			}
			if (got.Progress != nil) != tc.progress {
				t.Fatalf("progress = %+v, want present=%v", got.Progress, tc.progress)
			}
		})
	}
}
