package media

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/mindmesh/mindmesh/internal/platform/httpx"
)

func TestDownloadRoute_ErrorsGoThroughTheGatewayHandler(t *testing.T) {
	mux := runtime.NewServeMux(httpx.GatewayErrors(nil))
	if err := RegisterDownloadRoute(mux, nil); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/media/abc", nil))
	// The gateway's {code,message} shape proves the mux handler rendered it, not a hand-written body.
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":16`) {
		t.Fatalf("got %d %s, want 401 in the gateway's error shape", rec.Code, rec.Body.String())
	}
}
