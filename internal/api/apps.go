package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// AppStore is what the app endpoints need from persistence.
type AppStore interface {
	CreateApp(ctx context.Context, n store.NewApp) (store.App, error)
	UpdateApp(ctx context.Context, id string, set store.AppSettings) (store.App, error)
	AppByID(ctx context.Context, id string) (store.App, error)
	AppBySlug(ctx context.Context, slug string) (store.App, error)
	ListApps(ctx context.Context) ([]store.App, error)
	DeleteIdleApp(ctx context.Context, id string) error
}

const maxBodyBytes = 1 << 20

// duration is a time.Duration written as a Go duration string, e.g. "90s".
type duration time.Duration

func (d duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New(`must be a duration string such as "30s" or "2m"`)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q", s)
	}
	*d = duration(v)
	return nil
}

// appJSON is an app as the API returns it.
type appJSON struct {
	ID                   string    `json:"id"`
	Slug                 string    `json:"slug"`
	Repo                 string    `json:"repo"`
	Branch               string    `json:"branch"`
	DockerfilePath       string    `json:"dockerfile_path"`
	BuildContext         string    `json:"build_context"`
	Port                 int       `json:"port"`
	HealthPath           string    `json:"health_path"`
	HealthTimeout        duration  `json:"health_timeout"`
	CPULimit             float64   `json:"cpu_limit"`
	MemoryLimit          int64     `json:"memory_limit"`
	StopTimeout          duration  `json:"stop_timeout"`
	AutoDeploy           bool      `json:"auto_deploy"`
	GitHubInstallationID *int64    `json:"github_installation_id"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func toAppJSON(a store.App) appJSON {
	return appJSON{a.ID, a.Slug, a.RepoFullName, a.Branch, a.DockerfilePath, a.BuildContext, a.InternalPort,
		a.HealthPath, duration(a.HealthTimeout), a.CPULimit, a.MemoryLimit, duration(a.StopTimeout),
		a.AutoDeploy, a.GitHubInstallationID, a.CreatedAt, a.UpdatedAt}
}

// appRequest is the body of POST and PATCH. Absent fields are nil.
type appRequest struct {
	Slug                 *string   `json:"slug"`
	Repo                 *string   `json:"repo"`
	Branch               *string   `json:"branch"`
	DockerfilePath       *string   `json:"dockerfile_path"`
	BuildContext         *string   `json:"build_context"`
	Port                 *int      `json:"port"`
	HealthPath           *string   `json:"health_path"`
	HealthTimeout        *duration `json:"health_timeout"`
	CPULimit             *float64  `json:"cpu_limit"`
	MemoryLimit          *int64    `json:"memory_limit"`
	StopTimeout          *duration `json:"stop_timeout"`
	AutoDeploy           *bool     `json:"auto_deploy"`
	GitHubInstallationID *int64    `json:"github_installation_id"`
}

func (q appRequest) settings() app.Settings {
	return app.Settings{
		Branch: q.Branch, DockerfilePath: q.DockerfilePath, BuildContext: q.BuildContext,
		InternalPort: q.Port, HealthPath: q.HealthPath, HealthTimeout: (*time.Duration)(q.HealthTimeout),
		CPULimit: q.CPULimit, MemoryLimit: q.MemoryLimit, StopTimeout: (*time.Duration)(q.StopTimeout),
		AutoDeploy: q.AutoDeploy, GitHubInstallationID: q.GitHubInstallationID,
	}
}

func storeSettings(s app.Settings) store.AppSettings {
	return store.AppSettings{
		GitHubInstallationID: s.GitHubInstallationID, Branch: s.Branch, DockerfilePath: s.DockerfilePath,
		BuildContext: s.BuildContext, InternalPort: s.InternalPort, HealthPath: s.HealthPath,
		HealthTimeout: s.HealthTimeout, CPULimit: s.CPULimit, MemoryLimit: s.MemoryLimit,
		StopTimeout: s.StopTimeout, AutoDeploy: s.AutoDeploy,
	}
}

// decodeJSON reads a JSON object body strictly: JSON content type, at most
// 1 MiB, no unknown fields, nothing after the object.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return badRequest{http.StatusUnsupportedMediaType, "the body must be application/json"}
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return badRequest{http.StatusRequestEntityTooLarge, "the body is larger than 1 MiB"}
		}
		return badRequest{http.StatusBadRequest, "invalid JSON body: " + err.Error()}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return badRequest{http.StatusBadRequest, "the body must be a single JSON object"}
	}
	return nil
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type appHandlers struct {
	log  *slog.Logger
	apps AppStore
}

// lookup resolves {app}, which is an ID or a slug. The CLI uses slugs.
func (h *appHandlers) lookup(r *http.Request) (store.App, error) {
	ref := r.PathValue("app")
	if uuidRE.MatchString(ref) {
		a, err := h.apps.AppByID(r.Context(), ref)
		if !errors.Is(err, store.ErrNotFound) || !app.ValidSlug(ref) {
			return a, err
		}
	}
	if !app.ValidSlug(ref) {
		return store.App{}, store.ErrNotFound
	}
	return h.apps.AppBySlug(r.Context(), ref)
}

func (h *appHandlers) create(w http.ResponseWriter, r *http.Request) {
	var req appRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	var slug, repo string
	if req.Slug != nil {
		slug = *req.Slug
	}
	if req.Repo != nil {
		repo = *req.Repo
	}
	if err := app.ValidateNew(slug, repo, req.settings()); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	a, err := h.apps.CreateApp(r.Context(), store.NewApp{
		OwnerID: principal(r.Context()).UserID, Slug: slug, RepoFullName: repo, AppSettings: storeSettings(req.settings()),
	})
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	w.Header().Set("Location", "/v1/apps/"+a.ID)
	writeJSON(w, http.StatusCreated, "application/json", toAppJSON(a))
}

func (h *appHandlers) list(w http.ResponseWriter, r *http.Request) {
	apps, err := h.apps.ListApps(r.Context())
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	out := make([]appJSON, len(apps))
	for i, a := range apps {
		out[i] = toAppJSON(a)
	}
	writeJSON(w, http.StatusOK, "application/json", map[string]any{"apps": out})
}

func (h *appHandlers) get(w http.ResponseWriter, r *http.Request) {
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	writeJSON(w, http.StatusOK, "application/json", toAppJSON(a))
}

func (h *appHandlers) update(w http.ResponseWriter, r *http.Request) {
	var req appRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, h.log, err)
		return
	}
	var fe app.FieldErrors
	if req.Slug != nil {
		fe = append(fe, app.FieldError{Field: "slug", Detail: "cannot be changed"})
	}
	if req.Repo != nil {
		fe = append(fe, app.FieldError{Field: "repo", Detail: "cannot be changed; create a new app"})
	}
	if err := app.ValidateUpdate(req.settings()); err != nil {
		fe = append(fe, err.(app.FieldErrors)...)
	}
	if len(fe) > 0 {
		writeError(w, r, h.log, fe)
		return
	}
	cur, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	a, err := h.apps.UpdateApp(r.Context(), cur.ID, storeSettings(req.settings()))
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	writeJSON(w, http.StatusOK, "application/json", toAppJSON(a))
}

func (h *appHandlers) delete(w http.ResponseWriter, r *http.Request) {
	a, err := h.lookup(r)
	if err == nil {
		err = h.apps.DeleteIdleApp(r.Context(), a.ID)
	}
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
