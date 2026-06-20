package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.VersionResolver = (*HTTPVersionResolver)(nil)

// defaultRegistryBaseURLs maps each ecosystem to its public registry API root.
var defaultRegistryBaseURLs = map[workerdomain.Ecosystem]string{
	workerdomain.EcosystemNpm:      "https://registry.npmjs.org",
	workerdomain.EcosystemPyPI:     "https://pypi.org",
	workerdomain.EcosystemComposer: "https://repo.packagist.org",
	workerdomain.EcosystemMaven:    "https://search.maven.org",
	workerdomain.EcosystemNuGet:    "https://api.nuget.org",
	workerdomain.EcosystemRubyGems: "https://rubygems.org",
}

// ResolverOption is a functional option for HTTPVersionResolver.
type ResolverOption func(*HTTPVersionResolver)

// WithBaseURL overrides the registry base URL for one ecosystem. Used in tests
// to redirect requests to a local httptest.Server without touching production
// paths.
func WithBaseURL(ecosystem workerdomain.Ecosystem, baseURL string) ResolverOption {
	return func(r *HTTPVersionResolver) {
		r.baseURLs[ecosystem] = baseURL
	}
}

type resolverCacheKey struct {
	ecosystem workerdomain.Ecosystem
	pkg       string
}

// HTTPVersionResolver implements ports.VersionResolver by querying each
// ecosystem's public registry API over HTTP.
//
// Results are cached in-memory by (ecosystem, package) for the lifetime of the
// resolver instance. Sharing one resolver across multiple pipeline jobs avoids
// redundant registry roundtrips when the same popular library appears in many
// repositories.
type HTTPVersionResolver struct {
	client   *http.Client
	baseURLs map[workerdomain.Ecosystem]string
	mu       sync.Mutex
	cache    map[resolverCacheKey]workerdomain.VersionCurrency
}

// NewHTTPVersionResolver returns an HTTPVersionResolver. If client is nil a
// default client with a 10 s timeout is used.
func NewHTTPVersionResolver(client *http.Client, opts ...ResolverOption) *HTTPVersionResolver {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	urls := make(map[workerdomain.Ecosystem]string, len(defaultRegistryBaseURLs))
	for k, v := range defaultRegistryBaseURLs {
		urls[k] = v
	}
	r := &HTTPVersionResolver{
		client:   client,
		baseURLs: urls,
		cache:    make(map[resolverCacheKey]workerdomain.VersionCurrency),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *HTTPVersionResolver) Latest(ctx context.Context, ecosystem workerdomain.Ecosystem, pkg string) (workerdomain.VersionCurrency, error) {
	k := resolverCacheKey{ecosystem, pkg}

	r.mu.Lock()
	if entry, ok := r.cache[k]; ok {
		r.mu.Unlock()
		return entry, nil
	}
	r.mu.Unlock()

	result, err := r.fetchLatest(ctx, ecosystem, pkg)
	if err != nil {
		return workerdomain.VersionCurrency{}, err
	}

	r.mu.Lock()
	r.cache[k] = result
	r.mu.Unlock()
	return result, nil
}

func (r *HTTPVersionResolver) fetchLatest(ctx context.Context, ecosystem workerdomain.Ecosystem, pkg string) (workerdomain.VersionCurrency, error) {
	base, ok := r.baseURLs[ecosystem]
	if !ok {
		return workerdomain.VersionCurrency{}, fmt.Errorf("version resolver: unsupported ecosystem %q", ecosystem)
	}
	switch ecosystem {
	case workerdomain.EcosystemNpm:
		return r.fetchNpm(ctx, base, pkg)
	case workerdomain.EcosystemPyPI:
		return r.fetchPyPI(ctx, base, pkg)
	case workerdomain.EcosystemComposer:
		return r.fetchPackagist(ctx, base, pkg)
	case workerdomain.EcosystemMaven:
		return r.fetchMaven(ctx, base, pkg)
	case workerdomain.EcosystemNuGet:
		return r.fetchNuGet(ctx, base, pkg)
	case workerdomain.EcosystemRubyGems:
		return r.fetchRubyGems(ctx, base, pkg)
	default:
		return workerdomain.VersionCurrency{}, fmt.Errorf("version resolver: unsupported ecosystem %q", ecosystem)
	}
}

// ── per-registry fetch functions ──────────────────────────────────────────────

func (r *HTTPVersionResolver) fetchNpm(ctx context.Context, base, pkg string) (workerdomain.VersionCurrency, error) {
	// GET {base}/{package}/latest
	// Scoped packages (@scope/name) are safe in path segments.
	data, err := r.get(ctx, base+"/"+pkg+"/latest")
	if err != nil {
		return workerdomain.VersionCurrency{}, err
	}
	var resp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return workerdomain.VersionCurrency{}, fmt.Errorf("npm: parse %s: %w", pkg, err)
	}
	return workerdomain.VersionCurrency{LatestVersion: resp.Version}, nil
}

func (r *HTTPVersionResolver) fetchPyPI(ctx context.Context, base, pkg string) (workerdomain.VersionCurrency, error) {
	// GET {base}/pypi/{package}/json
	data, err := r.get(ctx, base+"/pypi/"+pkg+"/json")
	if err != nil {
		return workerdomain.VersionCurrency{}, err
	}
	var resp struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return workerdomain.VersionCurrency{}, fmt.Errorf("pypi: parse %s: %w", pkg, err)
	}
	return workerdomain.VersionCurrency{LatestVersion: resp.Info.Version}, nil
}

func (r *HTTPVersionResolver) fetchPackagist(ctx context.Context, base, pkg string) (workerdomain.VersionCurrency, error) {
	// GET {base}/p2/{vendor}/{package}.json
	// Versions are returned newest-first; take the first entry.
	parts := strings.SplitN(pkg, "/", 2)
	if len(parts) != 2 {
		return workerdomain.VersionCurrency{}, fmt.Errorf("packagist: invalid package %q (want vendor/package)", pkg)
	}
	data, err := r.get(ctx, base+"/p2/"+parts[0]+"/"+parts[1]+".json")
	if err != nil {
		return workerdomain.VersionCurrency{}, err
	}
	var resp struct {
		Packages map[string][]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return workerdomain.VersionCurrency{}, fmt.Errorf("packagist: parse %s: %w", pkg, err)
	}
	entries := resp.Packages[pkg]
	if len(entries) == 0 {
		return workerdomain.VersionCurrency{}, fmt.Errorf("packagist: no versions for %s", pkg)
	}
	return workerdomain.VersionCurrency{LatestVersion: entries[0].Version}, nil
}

func (r *HTTPVersionResolver) fetchMaven(ctx context.Context, base, pkg string) (workerdomain.VersionCurrency, error) {
	// GET {base}/solrsearch/select?q=g:{groupId}+AND+a:{artifactId}&rows=1&wt=json
	// pkg format: "groupId:artifactId"
	parts := strings.SplitN(pkg, ":", 2)
	if len(parts) != 2 {
		return workerdomain.VersionCurrency{}, fmt.Errorf("maven: invalid package %q (want groupId:artifactId)", pkg)
	}
	params := url.Values{}
	params.Set("q", "g:"+parts[0]+" AND a:"+parts[1])
	params.Set("rows", "1")
	params.Set("wt", "json")
	data, err := r.get(ctx, base+"/solrsearch/select?"+params.Encode())
	if err != nil {
		return workerdomain.VersionCurrency{}, err
	}
	var resp struct {
		Response struct {
			Docs []struct {
				LatestVersion string `json:"latestVersion"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return workerdomain.VersionCurrency{}, fmt.Errorf("maven: parse %s: %w", pkg, err)
	}
	if len(resp.Response.Docs) == 0 {
		return workerdomain.VersionCurrency{}, fmt.Errorf("maven: not found %s", pkg)
	}
	return workerdomain.VersionCurrency{LatestVersion: resp.Response.Docs[0].LatestVersion}, nil
}

func (r *HTTPVersionResolver) fetchNuGet(ctx context.Context, base, pkg string) (workerdomain.VersionCurrency, error) {
	// GET {base}/v3-flatcontainer/{package.lower()}/index.json
	// Latest = last element in the versions array.
	data, err := r.get(ctx, base+"/v3-flatcontainer/"+strings.ToLower(pkg)+"/index.json")
	if err != nil {
		return workerdomain.VersionCurrency{}, err
	}
	var resp struct {
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return workerdomain.VersionCurrency{}, fmt.Errorf("nuget: parse %s: %w", pkg, err)
	}
	if len(resp.Versions) == 0 {
		return workerdomain.VersionCurrency{}, fmt.Errorf("nuget: no versions for %s", pkg)
	}
	return workerdomain.VersionCurrency{LatestVersion: resp.Versions[len(resp.Versions)-1]}, nil
}

func (r *HTTPVersionResolver) fetchRubyGems(ctx context.Context, base, pkg string) (workerdomain.VersionCurrency, error) {
	// GET {base}/api/v1/gems/{gem}.json
	data, err := r.get(ctx, base+"/api/v1/gems/"+pkg+".json")
	if err != nil {
		return workerdomain.VersionCurrency{}, err
	}
	var resp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return workerdomain.VersionCurrency{}, fmt.Errorf("rubygems: parse %s: %w", pkg, err)
	}
	return workerdomain.VersionCurrency{LatestVersion: resp.Version}, nil
}

// ── HTTP helper ───────────────────────────────────────────────────────────────

func (r *HTTPVersionResolver) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(resp.Body)
}
