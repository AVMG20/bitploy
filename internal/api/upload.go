package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/auth"
)

const maxUploadBytes = 95 << 20 // under Cloudflare's 100 MB request cap

// upload handles POST /u/{id}: the raw zip body. The unguessable ID is the
// credential, so there is no other auth.
func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.ContentLength > maxUploadBytes {
		auth.WriteError(w, tooLarge())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	res, err := a.svc.ReceiveUpload(r.Context(), id, r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			err = tooLarge()
		}
		auth.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func tooLarge() *apperr.Error {
	return apperr.New(http.StatusRequestEntityTooLarge, "upload_too_large",
		"The upload is larger than 95 MB.",
		"Remove node_modules, .git, build output and other large folders from the zip, or deploy from a git repository.")
}
