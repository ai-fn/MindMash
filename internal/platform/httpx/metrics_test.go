package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServer_CountsRequestsByRouteTemplate(t *testing.T) {
	gw := runtime.NewServeMux(GatewayRoutes())
	if err := gw.HandlePath(http.MethodGet, "/v1/metric-decks/{deck_id}", func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
		w.WriteHeader(http.StatusTeapot)
	}); err != nil {
		t.Fatal(err)
	}
	root := http.NewServeMux()
	root.Handle("/v1/metric-stream", Route("/v1/metric-stream", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	root.Handle("/", gw)
	srv := New(DefaultConfig(":0"), Options{Handler: root})

	const deck = "/v1/metric-decks/{deck_id}"
	before := map[string]float64{
		"deck":     testutil.ToFloat64(httpRequests.WithLabelValues("GET", deck, "418")),
		"stream":   testutil.ToFloat64(httpRequests.WithLabelValues("GET", "/v1/metric-stream", "200")),
		"unrouted": testutil.ToFloat64(httpRequests.WithLabelValues("OTHER", unrouted, "404")),
	}
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/metric-decks/a", nil),
		httptest.NewRequest(http.MethodGet, "/v1/metric-decks/b", nil),
		httptest.NewRequest(http.MethodGet, "/v1/metric-stream", nil),
		httptest.NewRequest("BREW", "/v1/metric-nope/c", nil),
	} {
		srv.Handler().ServeHTTP(httptest.NewRecorder(), r)
	}

	if d := testutil.ToFloat64(httpRequests.WithLabelValues("GET", deck, "418")) - before["deck"]; d != 2 {
		t.Errorf("deck route count delta = %v, want 2 (ids must collapse into the template)", d)
	}
	if d := testutil.ToFloat64(httpRequests.WithLabelValues("GET", "/v1/metric-stream", "200")) - before["stream"]; d != 1 {
		t.Errorf("plain route count delta = %v, want 1", d)
	}
	if d := testutil.ToFloat64(httpRequests.WithLabelValues("OTHER", unrouted, "404")) - before["unrouted"]; d != 1 {
		t.Errorf("unrouted count delta = %v, want 1 (unknown paths and methods must not mint series)", d)
	}
	if n := testutil.CollectAndCount(httpDuration, "mindmesh_http_server_request_duration_seconds"); n == 0 {
		t.Error("no duration series recorded")
	}
}
