package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
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

// appUpload handles POST /api/v1/apps/{app}/upload: the raw zip as the request
// body, stored and deployed in one step. It sits behind the same authentication
// as the JSON API (bearer token, or session cookie with Origin and CSRF checks)
// but without its small body limit; the zip is capped at 95 MB.
func (a *API) appUpload(w http.ResponseWriter, r *http.Request) {
	act, err := auth.Require(r.Context())
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	if r.ContentLength > maxUploadBytes {
		drainBody(w, r)
		auth.WriteError(w, tooLarge())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	d, err := a.svc.UploadAndDeploy(r.Context(), act, r.PathValue("app"), r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			err = tooLarge()
		}
		drainBody(w, r)
		auth.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(d)
}
