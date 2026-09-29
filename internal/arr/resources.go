package arr

import (
	"context"
	"strconv"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

type RootFolderResource struct {
	Path       string `json:"path"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
	Accessible bool   `json:"accessible"`
}

type QualityProfileResource struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type TagResource struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

func ListRootFolders(ctx context.Context, client *httpclient.Client) ([]IntegrationRootFolder, error) {
	var resources []RootFolderResource
	if err := client.GetJSON(ctx, "/api/v3/rootfolder", &resources); err != nil {
		return nil, err
	}
	out := make([]IntegrationRootFolder, 0, len(resources))
	for _, resource := range resources {
		out = append(out, IntegrationRootFolder{
			Path:       resource.Path,
			FreeSpace:  resource.FreeSpace,
			TotalSpace: resource.TotalSpace,
			Accessible: resource.Accessible,
		})
	}
	return out, nil
}

func ListQualityProfiles(ctx context.Context, client *httpclient.Client) ([]IntegrationQualityProfile, error) {
	var resources []QualityProfileResource
	if err := client.GetJSON(ctx, "/api/v3/qualityprofile", &resources); err != nil {
		return nil, err
	}
	out := make([]IntegrationQualityProfile, 0, len(resources))
	for _, resource := range resources {
		out = append(out, IntegrationQualityProfile{
			ID:   resource.ID,
			Name: resource.Name,
		})
	}
	return out, nil
}

func ListTags(ctx context.Context, client *httpclient.Client) ([]IntegrationTag, error) {
	var resources []TagResource
	if err := client.GetJSON(ctx, "/api/v3/tag", &resources); err != nil {
		return nil, err
	}
	out := make([]IntegrationTag, 0, len(resources))
	for _, resource := range resources {
		out = append(out, IntegrationTag{
			ID:    resource.ID,
			Label: resource.Label,
		})
	}
	return out, nil
}

// AcceptedWithoutResponse returns a FulfillmentResult marking the submission
// as accepted by the downstream integration but with no external id captured
// (typically a 201 with empty body when lookup recovery also fails).
func AcceptedWithoutResponse(kind string) FulfillmentResult {
	return FulfillmentResult{
		IntegrationKind: kind,
		ExternalStatus:  "accepted_without_response",
	}
}

// downloadClientConfigResource is the part of GET /api/v3/config/downloadclient
// the plugin reads; Radarr and Sonarr serve the same shape.
type downloadClientConfigResource struct {
	// AutoRedownloadFailed is "Redownload Failed" under Failed Download
	// Handling: once a download fails, blocklist its release and search for
	// another. Both services default it on.
	AutoRedownloadFailed *bool `json:"autoRedownloadFailed"`
}

// redownloadsFailed reports whether the service searches for another release
// after a download fails. It assumes so when the setting cannot be read, so a
// transient error never fails a request the service may still recover.
func redownloadsFailed(ctx context.Context, client *httpclient.Client) bool {
	var config downloadClientConfigResource
	if err := client.GetJSON(ctx, "/api/v3/config/downloadclient", &config); err != nil {
		return true
	}
	return config.AutoRedownloadFailed == nil || *config.AutoRedownloadFailed
}

// StatusFromQueueEvaluation translates a QueueEvaluation into the
// FulfillmentStatus shape shared by Radarr and Sonarr clients. A failed
// download fails the target only when the service will not search for another
// release (redownload is false); while it searches, the target stays queued.
func StatusFromQueueEvaluation(kind string, externalID int, evaluation QueueEvaluation, redownload bool) FulfillmentStatus {
	status := FulfillmentStatus{
		Status:          StatusQueued,
		IntegrationKind: kind,
		ExternalID:      strconv.Itoa(externalID),
		ExternalStatus:  evaluation.ExternalStatus,
		Message:         evaluation.Message,
	}
	switch evaluation.State {
	case QueueStateDownloading:
		status.Status = StatusDownloading
	case QueueStateFailed:
		if redownload {
			status.Message += "; " + KindLabel(kind) + " is looking for another release"
		} else {
			status.Status = StatusFailed
		}
	}
	return status
}

// statusFromQueue evaluates a target's queue items and, unless a download has
// failed, reports how far its downloads are. Only a failed download costs a
// further call, to ask whether the service will search for another release.
func statusFromQueue(ctx context.Context, client *httpclient.Client, kind string, externalID int, queues []QueueResource) FulfillmentStatus {
	evaluation := EvaluateQueue(queues)
	if evaluation.State == QueueStateFailed {
		return StatusFromQueueEvaluation(kind, externalID, evaluation, redownloadsFailed(ctx, client))
	}
	status := StatusFromQueueEvaluation(kind, externalID, evaluation, false)
	status.Progress = EvaluateProgress(queues)
	return status
}
