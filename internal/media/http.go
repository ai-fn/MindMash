package media

import (
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
)

// RegisterDownloadRoute hand-registers the 302 to a presigned URL: generated gateway routes only
// return JSON.
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
