package media

import (
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// RegisterDownloadRoute wires GET /v1/media/{media_id} as a custom gateway route
// that 302-redirects to a short-lived presigned GET URL. grpc-gateway can only
// return JSON, so serving a redirect requires a hand-registered handler rather
// than a generated RPC. Identity is already on the request context (the auth
// middleware wraps this mux).
func RegisterDownloadRoute(mux *runtime.ServeMux, svc *Service) error {
	return mux.HandlePath("GET", "/v1/media/{media_id}",
		func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
			id, err := auth.RequireIdentity(r.Context())
			if err != nil {
				writeError(w, err)
				return
			}
			t := Tenant{UserID: id.UserID, ProfileID: id.ProfileID}

			url, err := svc.ResolveDownloadURL(r.Context(), t, pathParams["media_id"])
			if err != nil {
				writeError(w, err)
				return
			}
			http.Redirect(w, r, url, http.StatusFound)
		})
}

// writeError renders a domain error to the correct HTTP status with a small JSON
// body, matching the gateway's error shape closely enough for API clients.
func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(platformerrors.HTTPStatus(err))
	_, _ = w.Write([]byte(`{"error":` + jsonString(err.Error()) + `}`))
}

// jsonString quotes s as a JSON string.
func jsonString(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for _, c := range []byte(s) {
		switch c {
		case '"', '\\':
			b = append(b, '\\', c)
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			b = append(b, c)
		}
	}
	b = append(b, '"')
	return string(b)
}
