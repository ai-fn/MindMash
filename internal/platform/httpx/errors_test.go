package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	apperrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

func TestGatewayErrors_HidesCausesAndKeepsAppErrors(t *testing.T) {
	logs := observeLogs(t)
	secret := errors.New("pq: connection to 10.0.3.7 refused")
	var gotByNext error
	next := func(ctx context.Context, mux *runtime.ServeMux, m runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
		gotByNext = err
		runtime.DefaultHTTPErrorHandler(ctx, mux, m, w, r, err)
	}
	gw := runtime.NewServeMux(GatewayErrors(next))
	fail := func(path string, err error) {
		if herr := gw.HandlePath(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
			_, out := runtime.MarshalerForRequest(gw, r)
			runtime.HTTPError(r.Context(), gw, out, w, r, err)
		}); herr != nil {
			t.Fatal(herr)
		}
	}
	fail("/v1/internal", fmt.Errorf("repo: %w", secret))
	fail("/v1/missing", fmt.Errorf("svc: %w", apperrors.NotFound("deck not found", secret)))

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/internal", nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusInternalServerError || strings.Contains(string(body), "10.0.3.7") {
		t.Fatalf("internal: got %d %s, want 500 without the cause", rec.Code, body)
	}
	if logs.FilterMessage("httpx: internal error").Len() != 1 {
		t.Fatalf("want the cause logged once, got %v", logs.All())
	}

	rec = httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/missing", nil))
	body, _ = io.ReadAll(rec.Body)
	if rec.Code != http.StatusNotFound || !strings.Contains(string(body), "deck not found") || strings.Contains(string(body), "10.0.3.7") {
		t.Fatalf("not found: got %d %s", rec.Code, body)
	}
	var ae *apperrors.AppError
	if !errors.As(gotByNext, &ae) {
		t.Fatalf("next handler must still get the AppError (for localization), got %T", gotByNext)
	}
}
