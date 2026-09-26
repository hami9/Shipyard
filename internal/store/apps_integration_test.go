//go:build integration

package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hami9/shipyard/internal/store"
)

func ptr[T any](v T) *T { return &v }

func newStore(t *testing.T) (*store.Store, store.User, *pgxpool.Pool) {
	t.Helper()
	db := schemaDB(t)
	s := store.New(db)
	u, err := s.CreateUser(t.Context(), "admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return s, u, db
}

func minimalApp(owner, slug string) store.NewApp {
	return store.NewApp{OwnerID: owner, Slug: slug, RepoFullName: "hami9/demo",
		AppSettings: store.AppSettings{Branch: ptr("main"), InternalPort: ptr(3000)}}
}

func wantConstraint(t *testing.T, err, kind error, constraint string) {
	t.Helper()
	var ce *store.ConstraintError
	if !errors.Is(err, kind) || !errors.As(err, &ce) || (constraint != "" && ce.Constraint != constraint) {
		t.Fatalf("got %v, want %v on constraint %q", err, kind, constraint)
	}
}

func TestUsers(t *testing.T) {
	s, u, _ := newStore(t)
	if u.ID == "" || u.Role != "admin" {
		t.Fatalf("CreateUser = %+v", u)
	}
	got, err := s.UserByName(t.Context(), "admin")
	if err != nil || got.ID != u.ID {
		t.Fatalf("UserByName = %+v, %v", got, err)
	}
	if _, err := s.UserByName(t.Context(), "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: %v, want ErrNotFound", err)
	}
	_, err = s.CreateUser(t.Context(), "admin")
	wantConstraint(t, err, store.ErrConflict, "users_name_key")
	_, err = s.CreateUser(t.Context(), "Not Valid")
	wantConstraint(t, err, store.ErrInvalid, "users_name_check")
}

func TestCreateAppDefaults(t *testing.T) {
	s, u, _ := newStore(t)
	a, err := s.CreateApp(t.Context(), minimalApp(u.ID, "web"))
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	want := store.App{
		ID: a.ID, OwnerID: u.ID, Slug: "web", RepoFullName: "hami9/demo", Branch: "main",
		DockerfilePath: "Dockerfile", BuildContext: ".", InternalPort: 3000, HealthPath: "/",
		HealthTimeout: time.Minute, CPULimit: 1, MemoryLimit: 512 << 20, StopTimeout: 10 * time.Second,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("CreateApp defaults:\n got %+v\nwant %+v", a, want)
	}
	if a.ID == "" || a.CreatedAt.IsZero() {
		t.Fatalf("missing generated fields: %+v", a)
	}
}

func TestCreateAppRoundTrip(t *testing.T) {
	s, u, _ := newStore(t)
	ctx := t.Context()
	in := store.NewApp{OwnerID: u.ID, Slug: "api", RepoFullName: "hami9/api", AppSettings: store.AppSettings{
		GitHubInstallationID: ptr(int64(4242)), Branch: ptr("release/1.x"), DockerfilePath: ptr("docker/Dockerfile"),
		BuildContext: ptr("services/api"), InternalPort: ptr(8080), HealthPath: ptr("/healthz?deep=1"),
		HealthTimeout: ptr(90 * time.Second), CPULimit: ptr(1.5), MemoryLimit: ptr(int64(256 << 20)),
		StopTimeout: ptr(1500 * time.Millisecond), AutoDeploy: ptr(true),
	}}
	created, err := s.CreateApp(ctx, in)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	for name, get := range map[string]func() (store.App, error){
		"AppByID":   func() (store.App, error) { return s.AppByID(ctx, created.ID) },
		"AppBySlug": func() (store.App, error) { return s.AppBySlug(ctx, "api") },
	} {
		got, err := get()
		if err != nil || !reflect.DeepEqual(got, created) {
			t.Fatalf("%s:\n got %+v, %v\nwant %+v", name, got, err, created)
		}
	}
	if *created.GitHubInstallationID != 4242 || created.CPULimit != 1.5 || created.StopTimeout != 1500*time.Millisecond {
		t.Fatalf("optional fields not stored: %+v", created)
	}
}

func TestCreateAppRejects(t *testing.T) {
	s, u, _ := newStore(t)
	ctx := t.Context()
	if _, err := s.CreateApp(ctx, minimalApp(u.ID, "web")); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	tests := []struct {
		name       string
		edit       func(*store.NewApp)
		kind       error
		constraint string
	}{
		{"duplicate slug", func(*store.NewApp) {}, store.ErrConflict, "apps_slug_key"},
		{"bad slug", func(n *store.NewApp) { n.Slug = "Web_1" }, store.ErrInvalid, "apps_slug_check"},
		{"bad repo", func(n *store.NewApp) { n.Slug, n.RepoFullName = "x1", "not-a-repo" }, store.ErrInvalid, "apps_repo_full_name_check"},
		{"port range", func(n *store.NewApp) { n.Slug, n.InternalPort = "x2", ptr(70000) }, store.ErrInvalid, "apps_internal_port_check"},
		{"path escape", func(n *store.NewApp) { n.Slug, n.DockerfilePath = "x3", ptr("../Dockerfile") }, store.ErrInvalid, ""},
		{"memory too small", func(n *store.NewApp) { n.Slug, n.MemoryLimit = "x4", ptr(int64(1<<20)) }, store.ErrInvalid, "apps_memory_limit_check"},
		{"missing branch", func(n *store.NewApp) { n.Slug, n.Branch = "x5", nil }, store.ErrInvalid, ""},
		{"unknown owner", func(n *store.NewApp) { n.Slug, n.OwnerID = "x6", "00000000-0000-4000-8000-000000000000" }, store.ErrReference, "apps_owner_id_fkey"},
		{"malformed owner", func(n *store.NewApp) { n.Slug, n.OwnerID = "x7", "nope" }, store.ErrInvalid, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := minimalApp(u.ID, "web")
			tt.edit(&n)
			_, err := s.CreateApp(ctx, n)
			wantConstraint(t, err, tt.kind, tt.constraint)
		})
	}
}

func TestUpdateListDeleteApp(t *testing.T) {
	s, u, _ := newStore(t)
	ctx := t.Context()
	b, _ := s.CreateApp(ctx, minimalApp(u.ID, "b-app"))
	a, _ := s.CreateApp(ctx, minimalApp(u.ID, "a-app"))

	same, err := s.UpdateApp(ctx, a.ID, store.AppSettings{})
	if err != nil || !reflect.DeepEqual(same, a) {
		t.Fatalf("empty UpdateApp = %+v, %v; want unchanged", same, err)
	}
	up, err := s.UpdateApp(ctx, a.ID, store.AppSettings{Branch: ptr("dev"), HealthTimeout: ptr(5 * time.Second), AutoDeploy: ptr(true)})
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if up.Branch != "dev" || up.HealthTimeout != 5*time.Second || !up.AutoDeploy || up.InternalPort != 3000 {
		t.Fatalf("UpdateApp result %+v", up)
	}
	if !up.UpdatedAt.After(a.UpdatedAt) {
		t.Fatalf("UpdatedAt not bumped: %v -> %v", a.UpdatedAt, up.UpdatedAt)
	}
	_, err = s.UpdateApp(ctx, a.ID, store.AppSettings{HealthPath: ptr("no-slash")})
	wantConstraint(t, err, store.ErrInvalid, "apps_health_path_check")
	if _, err := s.UpdateApp(ctx, "00000000-0000-4000-8000-000000000000", store.AppSettings{Branch: ptr("x")}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateApp unknown id: %v, want ErrNotFound", err)
	}

	list, err := s.ListApps(ctx)
	if err != nil || len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("ListApps = %+v, %v; want [a-app b-app]", list, err)
	}

	if err := s.DeleteApp(ctx, a.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if err := s.DeleteApp(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second DeleteApp: %v, want ErrNotFound", err)
	}
	if _, err := s.AppByID(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AppByID after delete: %v, want ErrNotFound", err)
	}
	if _, err := s.AppByID(ctx, "nope"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("AppByID malformed: %v, want ErrInvalid", err)
	}
}

func TestInTx(t *testing.T) {
	s, u, _ := newStore(t)
	ctx := t.Context()
	boom := errors.New("boom")

	err := s.InTx(ctx, func(tx *store.Store) error {
		if _, err := tx.CreateApp(ctx, minimalApp(u.ID, "rolled-back")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want boom", err)
	}
	if _, err := s.AppBySlug(ctx, "rolled-back"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("app survived rollback: %v", err)
	}

	// A nested InTx joins the outer transaction, so its rollback covers both.
	err = s.InTx(ctx, func(tx *store.Store) error {
		if _, err := tx.CreateApp(ctx, minimalApp(u.ID, "outer")); err != nil {
			return err
		}
		return tx.InTx(ctx, func(inner *store.Store) error {
			_, err := inner.CreateApp(ctx, minimalApp(u.ID, "outer")) // duplicate slug
			return err
		})
	})
	wantConstraint(t, err, store.ErrConflict, "apps_slug_key")
	if _, err := s.AppBySlug(ctx, "outer"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outer insert survived: %v", err)
	}

	if err := s.InTx(ctx, func(tx *store.Store) error {
		_, err := tx.CreateApp(ctx, minimalApp(u.ID, "committed"))
		return err
	}); err != nil {
		t.Fatalf("InTx commit: %v", err)
	}
	if _, err := s.AppBySlug(ctx, "committed"); err != nil {
		t.Fatalf("committed app missing: %v", err)
	}
}

// Deleting a user that owns apps must fail rather than orphan them.
func TestOwnerDeleteRestricted(t *testing.T) {
	s, u, db := newStore(t)
	if _, err := s.CreateApp(t.Context(), minimalApp(u.ID, "web")); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	_, err := db.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, u.ID)
	if sqlState(err) != foreignKeyViolate {
		t.Fatalf("delete owner: %v, want foreign key violation", err)
	}
}
