package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusRecorder_KeepsFirstFinalCode(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	rec.WriteHeader(http.StatusEarlyHints)
	rec.WriteHeader(http.StatusNotFound)
	rec.WriteHeader(http.StatusInternalServerError)
	if rec.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.status)
	}
}
