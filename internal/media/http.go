package media

import (
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
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
				writeError(mux, w, r, err)
				return
			}
			t := Tenant{UserID: id.UserID, ProfileID: id.ProfileID}

			url, err := svc.ResolveDownloadURL(r.Context(), t, pathParams["media_id"])
			if err != nil {
				writeError(mux, w, r, err)
				return
			}
			http.Redirect(w, r, url, http.StatusFound)
		})
}

// writeError goes through the mux's error handler, so the response is sanitized and shaped like the gateway's.
func writeError(mux *runtime.ServeMux, w http.ResponseWriter, r *http.Request, err error) {
	_, out := runtime.MarshalerForRequest(mux, r)
	runtime.HTTPError(r.Context(), mux, out, w, r, err)
}
