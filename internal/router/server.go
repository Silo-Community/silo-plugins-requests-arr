package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Silo-Community/silo-plugins-requests-arr/internal/arr"
)

// Server implements the request_router.v1 RPCs over the plugin-local arr
// backend. It holds no state and stores no credentials; every call carries its
// own connections.
type Server struct {
	pluginv1.UnimplementedRequestRouterServer
}

// New returns a ready-to-serve request router.
func New() *Server { return &Server{} }

func descriptorToRequest(d *pluginv1.RequestDescriptor) arr.Request {
	if d == nil {
		return arr.Request{}
	}
	ids := d.GetExternalIds()
	r := arr.Request{
		MediaType: arr.MediaType(d.GetMediaType()),
		TMDBID:    atoiSafe(ids["tmdb"]),
		Title:     d.GetTitle(),
		Year:      int(d.GetYear()),
		IsAnime:   d.GetIsAnime(),
	}
	if v := ids["tvdb"]; v != "" {
		n := atoiSafe(v)
		r.TVDBID = &n
	}
	return r
}

func kindForMediaType(mt arr.MediaType) string {
	if mt == arr.MediaTypeSeries {
		return "sonarr"
	}
	return "radarr"
}

// Fulfill routes the request onto configured instances and submits one target
// per requested quality that has a matching default instance.
func (s *Server) Fulfill(ctx context.Context, req *pluginv1.FulfillRequest) (*pluginv1.FulfillResponse, error) {
	r := descriptorToRequest(req.GetRequest())
	instances := make([]arr.Instance, 0, len(req.GetConnections()))
	for _, c := range req.GetConnections() {
		instances = append(instances, instanceFromConnection(c))
	}

	qualities := make([]arr.RequestedQuality, 0, len(req.GetQualities()))
	for _, q := range req.GetQualities() {
		qualities = append(qualities, arr.RequestedQuality{ID: q.GetId(), Is4K: q.GetIs4K()})
	}
	planned := arr.RouteTargets(r, qualities, instances)
	if len(planned) == 0 {
		return &pluginv1.FulfillResponse{
			Message: fmt.Sprintf("no %s instance configured for the requested quality", kindForMediaType(r.MediaType)),
		}, nil
	}

	var targets []*pluginv1.FulfillmentTarget
	for _, pt := range planned {
		resolved := arr.ResolveInstance(pt)
		result, err := submit(ctx, r, resolved)
		t := &pluginv1.FulfillmentTarget{
			Quality:      pt.Quality,
			ConnectionId: resolved.ID,
		}
		if err != nil {
			t.Status = string(arr.StatusFailed)
			t.Message = err.Error()
		} else {
			t.Status = string(arr.StatusQueued)
			t.ExternalId = result.ExternalID
			t.ExternalStatus = result.ExternalStatus
		}
		targets = append(targets, t)
	}
	return &pluginv1.FulfillResponse{Targets: targets}, nil
}

func submit(ctx context.Context, r arr.Request, in arr.Instance) (arr.FulfillmentResult, error) {
	switch r.MediaType {
	case arr.MediaTypeMovie:
		return arr.NewRadarrClient(nil).SubmitMovie(ctx, r, in)
	case arr.MediaTypeSeries:
		return arr.NewSonarrClient(nil).SubmitSeries(ctx, r, in)
	default:
		return arr.FulfillmentResult{}, fmt.Errorf("unsupported media type %q", r.MediaType)
	}
}

// CheckStatus probes each target's external id against its connection and maps
// the arr status onto the proto. Connections that are missing or error out are
// skipped so one unreachable instance does not blank the whole response.
func (s *Server) CheckStatus(ctx context.Context, req *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	r := descriptorToRequest(req.GetRequest())
	byID := make(map[string]arr.Instance, len(req.GetConnections()))
	for _, c := range req.GetConnections() {
		in := instanceFromConnection(c)
		byID[in.ID] = in
	}

	var statuses []*pluginv1.TargetStatus
	for _, tref := range req.GetTargets() {
		in, ok := byID[tref.GetConnectionId()]
		if !ok {
			continue
		}
		probe := r
		probe.ExternalID = tref.GetExternalId()

		var st arr.FulfillmentStatus
		var err error
		switch r.MediaType {
		case arr.MediaTypeMovie:
			st, err = arr.NewRadarrClient(nil).CheckMovieStatus(ctx, probe, in)
		case arr.MediaTypeSeries:
			st, err = arr.NewSonarrClient(nil).CheckSeriesStatus(ctx, probe, in)
		default:
			continue
		}
		if err != nil {
			continue
		}
		statuses = append(statuses, &pluginv1.TargetStatus{
			Quality:        tref.GetQuality(),
			ConnectionId:   tref.GetConnectionId(),
			Status:         string(st.Status),
			ExternalStatus: st.ExternalStatus,
			Message:        st.Message,
		})
	}
	return &pluginv1.CheckStatusResponse{Statuses: statuses}, nil
}

// validateDetectTimeout bounds the detection Validate runs on save, so a slow
// server cannot hold the save for the HTTP client's full timeout.
const validateDetectTimeout = 10 * time.Second

func detectService(ctx context.Context, in arr.Instance) (kind, label string, err error) {
	return arr.DetectService(ctx, httpclient.New(in.BaseURL, in.APIKeyRef, nil))
}

// ListConfigOptions returns the selectable root folders / quality profiles /
// tags for a connection, keyed by both the standard and anime config fields.
// It first asks the server whether it is Sonarr or Radarr, reads the options
// the way that service serves them, and reports what it found as the single
// service_kind option, labelled with the name and version.
func (s *Server) ListConfigOptions(ctx context.Context, req *pluginv1.ListConfigOptionsRequest) (*pluginv1.ListConfigOptionsResponse, error) {
	in := instanceFromConnection(req.GetConnection())
	kind, label, err := detectService(ctx, in)
	if err != nil {
		return nil, err
	}
	in.Kind = kind

	var opts *arr.IntegrationOptions
	if in.Kind == "sonarr" {
		opts, err = arr.NewSonarrClient(nil).ListSeriesIntegrationOptions(ctx, in)
	} else {
		opts, err = arr.NewRadarrClient(nil).ListMovieIntegrationOptions(ctx, in)
	}
	if err != nil {
		return nil, err
	}

	rf := &pluginv1.ConfigOptionList{}
	for _, f := range opts.RootFolders {
		// Value stays the bare path (stored in plugin_config and sent to arr);
		// only the label surfaces the free-space / accessibility hint.
		rf.Options = append(rf.Options, &pluginv1.ConfigOption{Value: f.Path, Label: rootFolderLabel(f)})
	}
	qp := &pluginv1.ConfigOptionList{}
	for _, p := range opts.QualityProfiles {
		qp.Options = append(qp.Options, &pluginv1.ConfigOption{Value: itoa(p.ID), Label: p.Name})
	}
	tg := &pluginv1.ConfigOptionList{}
	for _, t := range opts.Tags {
		tg.Options = append(tg.Options, &pluginv1.ConfigOption{Value: itoa(t.ID), Label: t.Label})
	}

	return &pluginv1.ListConfigOptionsResponse{
		OptionsByField: map[string]*pluginv1.ConfigOptionList{
			"root_folder":              rf,
			"anime_root_folder":        rf,
			"quality_profile_id":       qp,
			"anime_quality_profile_id": qp,
			"tags":                     tg,
			"anime_tags":               tg,
			"service_kind":             {Options: []*pluginv1.ConfigOption{{Value: kind, Label: label}}},
		},
	}, nil
}

// TestConnection verifies the plugin can reach the arr instance by detecting
// the service and listing its integration options.
func (s *Server) TestConnection(ctx context.Context, req *pluginv1.TestConnectionRequest) (*pluginv1.TestConnectionResponse, error) {
	in := instanceFromConnection(req.GetConnection())

	kind, label, err := detectService(ctx, in)
	if err != nil {
		return &pluginv1.TestConnectionResponse{Ok: false, Message: statusMessage(err)}, nil
	}
	in.Kind = kind
	if in.Kind == "sonarr" {
		_, err = arr.NewSonarrClient(nil).ListSeriesIntegrationOptions(ctx, in)
	} else {
		_, err = arr.NewRadarrClient(nil).ListMovieIntegrationOptions(ctx, in)
	}
	if err != nil {
		return &pluginv1.TestConnectionResponse{Ok: false, Message: err.Error()}, nil
	}
	return &pluginv1.TestConnectionResponse{Ok: true, Message: "Connected to " + label}, nil
}

// statusMessage is a gRPC status's message, or the error text.
func statusMessage(err error) string {
	if st, ok := status.FromError(err); ok {
		return st.Message()
	}
	return err.Error()
}

// Validate runs cross-field consistency checks on a single connection's config.
// A connection cannot be the HD default while flagged as a 4K server, and the
// 4K default must actually be a 4K server. Errors are returned per-field so the
// schema-driven config form can highlight the offending toggles.
func (s *Server) Validate(ctx context.Context, req *pluginv1.ValidateRequest) (*pluginv1.ValidateResponse, error) {
	in := instanceFromConnection(req.GetConnection())
	fieldErrors := map[string]string{}
	if in.IsDefault && in.Is4K {
		fieldErrors["is_default"] = "the HD default cannot be a 4K server"
	}
	if in.IsDefault4K && !in.Is4K {
		fieldErrors["is_default_4k"] = "the 4K default must be a 4K server"
	}
	// The service chosen must be the one at the address. Detection that fails
	// for any other reason does not block the save: the options probe already
	// reports whether the server can be reached.
	if strings.TrimSpace(in.BaseURL) != "" && strings.TrimSpace(in.APIKeyRef) != "" {
		detectCtx, cancel := context.WithTimeout(ctx, validateDetectTimeout)
		kind, _, err := detectService(detectCtx, in)
		cancel()
		switch {
		case err == nil && kind != in.Kind:
			fieldErrors["service_kind"] = fmt.Sprintf("This server is %s.", arr.KindLabel(kind))
		case status.Code(err) == codes.InvalidArgument:
			fieldErrors["service_kind"] = statusMessage(err)
		}
	}
	// One default per service_kind, per tier. Compare only against siblings of
	// the same kind; a config-less or different-kind sibling never conflicts.
	for _, sib := range req.GetSiblings() {
		if sib.GetId() == req.GetConnection().GetId() {
			continue
		}
		other := instanceFromConnection(sib)
		if other.Kind != in.Kind {
			continue
		}
		if in.IsDefault && other.IsDefault {
			fieldErrors["is_default"] = fmt.Sprintf("%s already has an HD default; unset it on the other connection first", in.Kind)
		}
		if in.IsDefault4K && other.IsDefault4K {
			fieldErrors["is_default_4k"] = fmt.Sprintf("%s already has a 4K default; unset it on the other connection first", in.Kind)
		}
	}
	return &pluginv1.ValidateResponse{FieldErrors: fieldErrors}, nil
}

// rootFolderLabel renders a human-friendly option label for a root folder,
// surfacing the accessibility / free-space hint the admin UI used to show.
// The option Value remains the bare path; only the label is decorated.
func rootFolderLabel(rf arr.IntegrationRootFolder) string {
	if !rf.Accessible {
		return rf.Path + " (inaccessible)"
	}
	if rf.FreeSpace > 0 {
		return fmt.Sprintf("%s (%s free)", rf.Path, humanizeBytes(rf.FreeSpace))
	}
	return rf.Path
}

// humanizeBytes formats a byte count using binary (1024-based) units with one
// decimal place, e.g. 1610612736 -> "1.5 GiB".
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
