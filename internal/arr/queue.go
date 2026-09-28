package arr

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

type QueueResource struct {
	ID       int `json:"id,omitempty"`
	MovieID  int `json:"movieId,omitempty"`
	SeriesID int `json:"seriesId,omitempty"`
	// SeasonNumber is the Sonarr season the download belongs to; nil when
	// the server does not report one.
	SeasonNumber          *int   `json:"seasonNumber,omitempty"`
	Title                 string `json:"title,omitempty"`
	Status                string `json:"status,omitempty"`
	TrackedDownloadStatus string `json:"trackedDownloadStatus,omitempty"`
	TrackedDownloadState  string `json:"trackedDownloadState,omitempty"`
	DownloadID            string `json:"downloadId,omitempty"`
	OutputPath            string `json:"outputPath,omitempty"`
	// Size and SizeLeft are bytes, which the arr reports as decimals; 0 when
	// the download client does not know the size yet. Case-insensitive
	// decoding also accepts sizeLeft, the name Sonarr v5 moves to.
	Size     float64 `json:"size,omitempty"`
	SizeLeft float64 `json:"sizeleft,omitempty"`
	// EstimatedCompletionTime is nil when the download client has no estimate.
	EstimatedCompletionTime *time.Time `json:"estimatedCompletionTime,omitempty"`
}

type QueueEvaluation struct {
	State          string
	ExternalStatus string
	Message        string
}

const (
	QueueStateQueued      = "queued"
	QueueStateDownloading = "downloading"
	QueueStateFailed      = "failed"
)

func EvaluateQueue(resources []QueueResource) QueueEvaluation {
	if len(resources) == 0 {
		return QueueEvaluation{State: QueueStateQueued, ExternalStatus: "not_in_queue"}
	}

	queued := false
	downloadingStatus := ""
	for _, resource := range resources {
		status := strings.TrimSpace(resource.Status)
		state := strings.TrimSpace(resource.TrackedDownloadState)
		externalStatus := joinStatus(status, state)

		if status == "failed" || state == "failed" || state == "failedPending" {
			return QueueEvaluation{
				State:          QueueStateFailed,
				ExternalStatus: externalStatus,
				Message:        queueFailureMessage(resource, externalStatus),
			}
		}
		// A download the arr is importing, waiting to import, or cannot
		// import is still in flight.
		phase := queuePhase(resource)
		if status == "downloading" || state == "downloading" || phase == PhaseImporting || phase == PhaseImportBlocked {
			if downloadingStatus == "" {
				downloadingStatus = externalStatus
			}
			queued = true
			continue
		}
		queued = true
	}
	if downloadingStatus != "" {
		return QueueEvaluation{State: QueueStateDownloading, ExternalStatus: downloadingStatus}
	}
	if queued {
		return QueueEvaluation{State: QueueStateQueued, ExternalStatus: "queued"}
	}
	return QueueEvaluation{State: QueueStateQueued, ExternalStatus: "unknown"}
}

// Download phases, as the host names them.
const (
	PhaseQueued        = "queued"
	PhaseDownloading   = "downloading"
	PhasePaused        = "paused"
	PhaseStalled       = "stalled"
	PhaseImporting     = "importing"
	PhaseImportBlocked = "import_blocked"
)

// phasePrecedence orders phases for aggregation, first wins: a download that
// needs attention outranks the rest, then one that is still downloading.
var phasePrecedence = []string{
	PhaseImportBlocked,
	PhaseStalled,
	PhaseDownloading,
	PhaseImporting,
	PhasePaused,
	PhaseQueued,
}

// Progress is how far a target's downloads are.
type Progress struct {
	Phase string
	// BytesTotal and BytesLeft are 0 when any download does not know its
	// size yet: summing only the known sizes would overstate the percentage.
	BytesTotal int64
	BytesLeft  int64
	// EstimatedCompletion is the latest estimate; nil when none has one.
	EstimatedCompletion *time.Time
	// Downloads counts the distinct downloads.
	Downloads int
}

// EvaluateProgress sums the queue items into one figure, or returns nil for an
// empty queue. Sonarr lists a season pack once per episode, each entry
// carrying the whole pack's size, so items are counted once per download.
func EvaluateProgress(resources []QueueResource) *Progress {
	if len(resources) == 0 {
		return nil
	}
	progress := &Progress{Phase: PhaseQueued}
	seen := make(map[string]bool, len(resources))
	sizeUnknown := false
	for i, resource := range resources {
		progress.Phase = higherPhase(progress.Phase, queuePhase(resource))
		if eta := resource.EstimatedCompletionTime; eta != nil {
			if progress.EstimatedCompletion == nil || eta.After(*progress.EstimatedCompletion) {
				progress.EstimatedCompletion = eta
			}
		}

		key := downloadKey(resource, i)
		if seen[key] {
			continue
		}
		seen[key] = true
		progress.Downloads++
		size := roundBytes(resource.Size)
		if size <= 0 {
			sizeUnknown = true
			continue
		}
		progress.BytesTotal += size
		progress.BytesLeft += min(max(roundBytes(resource.SizeLeft), 0), size)
	}
	if sizeUnknown {
		progress.BytesTotal, progress.BytesLeft = 0, 0
	}
	return progress
}

// downloadKey identifies the download a queue item belongs to: its downloadId,
// else the release's title and size, else the queue item id. A release the arr
// holds back (a delay profile, an unavailable download client) has no
// downloadId yet, and Sonarr lists it once per episode under a different id,
// with the same title and size. An item with none of these counts as its own
// download.
func downloadKey(resource QueueResource, index int) string {
	if id := strings.TrimSpace(resource.DownloadID); id != "" {
		return "download:" + id
	}
	if title := strings.TrimSpace(resource.Title); title != "" {
		return "pending:" + title + "|" + strconv.FormatInt(roundBytes(resource.Size), 10)
	}
	if resource.ID != 0 {
		return "item:" + strconv.Itoa(resource.ID)
	}
	return "index:" + strconv.Itoa(index)
}

// queuePhase maps one queue item to a download phase. A stalled torrent
// reports status warning; usenet clients report no stall.
func queuePhase(resource QueueResource) string {
	status := strings.TrimSpace(resource.Status)
	state := strings.TrimSpace(resource.TrackedDownloadState)
	trackedStatus := strings.TrimSpace(resource.TrackedDownloadStatus)
	troubled := trackedStatus == "warning" || trackedStatus == "error"
	// A download the client has finished stays in the downloading state until
	// the arr takes it up for import. Radarr and Sonarr leave it there, with a
	// warning, when they cannot (a missing remote path mapping, say).
	awaitingImport := state == "importPending" || (status == "completed" && state == "downloading")
	switch {
	case state == "importBlocked" || (awaitingImport && troubled):
		return PhaseImportBlocked
	case awaitingImport || state == "importing":
		return PhaseImporting
	case status == "paused":
		return PhasePaused
	case status == "warning" || (status == "downloading" && troubled):
		return PhaseStalled
	case status == "downloading":
		return PhaseDownloading
	default:
		return PhaseQueued
	}
}

// higherPhase returns whichever phase ranks first in phasePrecedence.
func higherPhase(a, b string) string {
	if slices.Index(phasePrecedence, b) < slices.Index(phasePrecedence, a) {
		return b
	}
	return a
}

func roundBytes(n float64) int64 {
	return int64(math.Round(n))
}

func joinStatus(status, state string) string {
	switch {
	case status != "" && state != "":
		return status + "/" + state
	case status != "":
		return status
	case state != "":
		return state
	default:
		return "unknown"
	}
}

func queueFailureMessage(resource QueueResource, externalStatus string) string {
	title := strings.TrimSpace(resource.Title)
	if title == "" {
		return "external queue failed: " + externalStatus
	}
	return "external queue failed for " + title + ": " + externalStatus
}
