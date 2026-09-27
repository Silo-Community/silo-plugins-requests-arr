package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// arrServer answers system/status as appName (or with statusCode when it is
// not 200) and serves one root folder, quality profile and tag.
func arrServer(t *testing.T, appName string, statusCode int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			http.Error(w, `{"message":"Unauthorized"}`, statusCode)
			return
		}
		switch r.URL.Path {
		case "/api/v3/system/status":
			_, _ = w.Write([]byte(`{"appName":"` + appName + `","version":"4.0.14.2939"}`))
		case "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"path":"/tv","accessible":true}]`))
		case "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"HD"}]`))
		case "/api/v3/tag":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func connTo(t *testing.T, baseURL, kind string) *pluginv1.RouterConnection {
	t.Helper()
	cfg, err := structpb.NewStruct(map[string]any{"service_kind": kind})
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	return &pluginv1.RouterConnection{Id: "c1", BaseUrl: baseURL, ApiKey: "k", Config: cfg}
}

func TestListConfigOptionsReportsDetectedService(t *testing.T) {
	for _, tc := range []struct{ app, kind, label string }{
		{"Sonarr", "sonarr", "Sonarr 4.0.14.2939"},
		{"Radarr", "radarr", "Radarr 4.0.14.2939"},
	} {
		t.Run(tc.app, func(t *testing.T) {
			srv := arrServer(t, tc.app, http.StatusOK)
			// The saved kind is the other one: detection wins.
			other := "radarr"
			if tc.kind == "radarr" {
				other = "sonarr"
			}
			resp, err := New().ListConfigOptions(context.Background(), &pluginv1.ListConfigOptionsRequest{Connection: connTo(t, srv.URL, other)})
			if err != nil {
				t.Fatalf("ListConfigOptions: %v", err)
			}
			got := resp.GetOptionsByField()["service_kind"].GetOptions()
			if len(got) != 1 || got[0].GetValue() != tc.kind || got[0].GetLabel() != tc.label {
				t.Fatalf("service_kind options = %v, want one %s %q", got, tc.kind, tc.label)
			}
			if n := len(resp.GetOptionsByField()["quality_profile_id"].GetOptions()); n != 1 {
				t.Fatalf("quality profiles = %d, want 1", n)
			}
		})
	}
}

func TestListConfigOptionsRefusesOtherApps(t *testing.T) {
	srv := arrServer(t, "Whisparr", http.StatusOK)
	_, err := New().ListConfigOptions(context.Background(), &pluginv1.ListConfigOptionsRequest{Connection: connTo(t, srv.URL, "sonarr")})
	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "This is Whisparr, not Sonarr or Radarr." {
		t.Fatalf("err = %v, want InvalidArgument naming Whisparr", err)
	}
}

// HTTP failures reach the host unchanged, so it can say a key was rejected.
func TestListConfigOptionsPassesHTTPErrorsThrough(t *testing.T) {
	srv := arrServer(t, "", http.StatusUnauthorized)
	_, err := New().ListConfigOptions(context.Background(), &pluginv1.ListConfigOptionsRequest{Connection: connTo(t, srv.URL, "sonarr")})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || status.Code(err) == codes.InvalidArgument {
		t.Fatalf("err = %v, want the HTTP 401 error", err)
	}
}

func TestValidateRefusesKindThatDoesNotMatchServer(t *testing.T) {
	sonarr := arrServer(t, "Sonarr", http.StatusOK)
	resp, err := New().Validate(context.Background(), &pluginv1.ValidateRequest{CapabilityId: "arr", Connection: connTo(t, sonarr.URL, "radarr")})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := resp.GetFieldErrors()["service_kind"]; got != "This server is Sonarr." {
		t.Fatalf("service_kind error = %q, want mismatch", got)
	}

	resp, _ = New().Validate(context.Background(), &pluginv1.ValidateRequest{CapabilityId: "arr", Connection: connTo(t, sonarr.URL, "sonarr")})
	if got := resp.GetFieldErrors()["service_kind"]; got != "" {
		t.Fatalf("matching kind got error %q", got)
	}

	lidarr := arrServer(t, "Lidarr", http.StatusOK)
	resp, _ = New().Validate(context.Background(), &pluginv1.ValidateRequest{CapabilityId: "arr", Connection: connTo(t, lidarr.URL, "sonarr")})
	if got := resp.GetFieldErrors()["service_kind"]; got != "This is Lidarr, not Sonarr or Radarr." {
		t.Fatalf("other app error = %q", got)
	}
}

// A server Validate cannot read does not block the save.
func TestValidateIgnoresDetectionFailures(t *testing.T) {
	srv := arrServer(t, "", http.StatusUnauthorized)
	resp, err := New().Validate(context.Background(), &pluginv1.ValidateRequest{CapabilityId: "arr", Connection: connTo(t, srv.URL, "radarr")})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(resp.GetFieldErrors()) != 0 {
		t.Fatalf("field errors = %v, want none", resp.GetFieldErrors())
	}
}

func TestTestConnectionNamesTheService(t *testing.T) {
	srv := arrServer(t, "Sonarr", http.StatusOK)
	resp, err := New().TestConnection(context.Background(), &pluginv1.TestConnectionRequest{Connection: connTo(t, srv.URL, "radarr")})
	if err != nil || !resp.GetOk() || resp.GetMessage() != "Connected to Sonarr 4.0.14.2939" {
		t.Fatalf("TestConnection = %v, %v", resp, err)
	}
}
