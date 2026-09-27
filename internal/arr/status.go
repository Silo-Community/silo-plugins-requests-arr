package arr

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxAppNameLength bounds the server-reported name echoed back in an error.
const maxAppNameLength = 40

type systemStatusResource struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
}

// DetectService asks the server what it is. Sonarr and Radarr both answer
// GET /api/v3/system/status with their name and version; kind is "sonarr" or
// "radarr" and label reads like "Sonarr 4.0.14". Another app answers with an
// InvalidArgument status whose message names it, which the host shows as is.
// HTTP and transport errors are returned unchanged so the host can say what
// went wrong (a rejected key, a wrong URL, nothing listening).
func DetectService(ctx context.Context, client *httpclient.Client) (kind, label string, err error) {
	var resource systemStatusResource
	if err := client.GetJSON(ctx, "/api/v3/system/status", &resource); err != nil {
		return "", "", err
	}
	app := strings.TrimSpace(resource.AppName)
	var name string
	switch strings.ToLower(app) {
	case "sonarr":
		kind, name = "sonarr", "Sonarr"
	case "radarr":
		kind, name = "radarr", "Radarr"
	case "":
		return "", "", status.Error(codes.InvalidArgument, "That address answered, but not as Sonarr or Radarr.")
	default:
		return "", "", status.Error(codes.InvalidArgument, fmt.Sprintf("This is %s, not Sonarr or Radarr.", truncateName(app)))
	}
	label = name
	if version := strings.TrimSpace(resource.Version); version != "" {
		label += " " + truncateName(version)
	}
	return kind, label, nil
}

// KindLabel is the display name for a service kind: "Sonarr", "Radarr", or
// the kind itself.
func KindLabel(kind string) string {
	switch kind {
	case "sonarr":
		return "Sonarr"
	case "radarr":
		return "Radarr"
	}
	return kind
}

func truncateName(s string) string {
	if utf8.RuneCountInString(s) <= maxAppNameLength {
		return s
	}
	return string([]rune(s)[:maxAppNameLength]) + "…"
}
