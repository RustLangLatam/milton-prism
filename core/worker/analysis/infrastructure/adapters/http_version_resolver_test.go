package adapters_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestResolver returns a resolver pointing at a single httptest server for
// the given ecosystem. The caller is responsible for closing srv.
func newTestResolver(ecosystem workerdomain.Ecosystem, srv *httptest.Server) *adapters.HTTPVersionResolver {
	return adapters.NewHTTPVersionResolver(srv.Client(), adapters.WithBaseURL(ecosystem, srv.URL))
}

// ── npm ───────────────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_Npm_FetchesLatest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/express/latest", r.URL.Path)
		fmt.Fprint(w, `{"version":"4.18.2","name":"express"}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemNpm, srv)
	cur, err := r.Latest(context.Background(), workerdomain.EcosystemNpm, "express")
	require.NoError(t, err)
	assert.Equal(t, "4.18.2", cur.LatestVersion)
}

func TestHTTPVersionResolver_Npm_ScopedPackage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/@nestjs/core/latest", r.URL.Path)
		fmt.Fprint(w, `{"version":"10.3.0"}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemNpm, srv)
	cur, err := r.Latest(context.Background(), workerdomain.EcosystemNpm, "@nestjs/core")
	require.NoError(t, err)
	assert.Equal(t, "10.3.0", cur.LatestVersion)
}

// ── PyPI ──────────────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_PyPI_FetchesLatest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/pypi/flask/json", r.URL.Path)
		fmt.Fprint(w, `{"info":{"version":"3.0.3","name":"flask"}}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemPyPI, srv)
	cur, err := r.Latest(context.Background(), workerdomain.EcosystemPyPI, "flask")
	require.NoError(t, err)
	assert.Equal(t, "3.0.3", cur.LatestVersion)
}

// ── Packagist ─────────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_Packagist_FetchesLatest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/p2/laravel/framework.json", r.URL.Path)
		fmt.Fprint(w, `{"packages":{"laravel/framework":[{"version":"v11.0.5"},{"version":"v11.0.0"}]}}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemComposer, srv)
	cur, err := r.Latest(context.Background(), workerdomain.EcosystemComposer, "laravel/framework")
	require.NoError(t, err)
	// First entry in the array is the latest.
	assert.Equal(t, "v11.0.5", cur.LatestVersion)
}

func TestHTTPVersionResolver_Packagist_InvalidPackageName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemComposer, srv)
	_, err := r.Latest(context.Background(), workerdomain.EcosystemComposer, "no-slash-here")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vendor/package")
}

// ── Maven ─────────────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_Maven_FetchesLatest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/solrsearch/select", r.URL.Path)
		assert.Contains(t, r.URL.RawQuery, "spring-boot-starter-web")
		fmt.Fprint(w, `{"response":{"docs":[{"latestVersion":"3.2.5"}]}}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemMaven, srv)
	cur, err := r.Latest(context.Background(), workerdomain.EcosystemMaven,
		"org.springframework.boot:spring-boot-starter-web")
	require.NoError(t, err)
	assert.Equal(t, "3.2.5", cur.LatestVersion)
}

// ── NuGet ─────────────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_NuGet_FetchesLatest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Package name is lowercased in the URL.
		assert.Equal(t, "/v3-flatcontainer/newtonsoft.json/index.json", r.URL.Path)
		fmt.Fprint(w, `{"versions":["12.0.0","13.0.0","13.0.3"]}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemNuGet, srv)
	cur, err := r.Latest(context.Background(), workerdomain.EcosystemNuGet, "Newtonsoft.Json")
	require.NoError(t, err)
	// Latest = last element in versions array.
	assert.Equal(t, "13.0.3", cur.LatestVersion)
}

// ── RubyGems ──────────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_RubyGems_FetchesLatest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/gems/rails.json", r.URL.Path)
		fmt.Fprint(w, `{"version":"7.1.3","name":"rails"}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemRubyGems, srv)
	cur, err := r.Latest(context.Background(), workerdomain.EcosystemRubyGems, "rails")
	require.NoError(t, err)
	assert.Equal(t, "7.1.3", cur.LatestVersion)
}

// ── cache ─────────────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_CachePreventsSecondRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"version":"4.18.2"}`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemNpm, srv)
	ctx := context.Background()

	_, err := r.Latest(ctx, workerdomain.EcosystemNpm, "express")
	require.NoError(t, err)
	_, err = r.Latest(ctx, workerdomain.EcosystemNpm, "express")
	require.NoError(t, err)

	assert.Equal(t, int32(1), requests.Load(), "second call must be served from cache")
}

func TestHTTPVersionResolver_CacheKeyIncludesEcosystem(t *testing.T) {
	t.Parallel()
	// Same package name in two ecosystems should be cached independently.
	var npmReqs, pypiReqs atomic.Int32
	npmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		npmReqs.Add(1)
		fmt.Fprint(w, `{"version":"1.0.0"}`)
	}))
	defer npmSrv.Close()
	pypiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pypiReqs.Add(1)
		fmt.Fprint(w, `{"info":{"version":"2.0.0"}}`)
	}))
	defer pypiSrv.Close()

	r2 := adapters.NewHTTPVersionResolver(npmSrv.Client(),
		adapters.WithBaseURL(workerdomain.EcosystemNpm, npmSrv.URL),
		adapters.WithBaseURL(workerdomain.EcosystemPyPI, pypiSrv.URL),
	)
	ctx := context.Background()

	_, _ = r2.Latest(ctx, workerdomain.EcosystemNpm, "requests")
	_, _ = r2.Latest(ctx, workerdomain.EcosystemPyPI, "requests")

	assert.Equal(t, int32(1), npmReqs.Load())
	assert.Equal(t, int32(1), pypiReqs.Load())
}

// ── error handling ────────────────────────────────────────────────────────────

func TestHTTPVersionResolver_HttpError_ReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemNpm, srv)
	_, err := r.Latest(context.Background(), workerdomain.EcosystemNpm, "nonexistent-pkg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestHTTPVersionResolver_MalformedJSON_ReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	r := newTestResolver(workerdomain.EcosystemNpm, srv)
	_, err := r.Latest(context.Background(), workerdomain.EcosystemNpm, "express")
	require.Error(t, err)
}

func TestHTTPVersionResolver_UnsupportedEcosystem_ReturnsError(t *testing.T) {
	t.Parallel()
	r := adapters.NewHTTPVersionResolver(nil)
	_, err := r.Latest(context.Background(), "UnknownEcosystem", "some-pkg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported ecosystem")
}
