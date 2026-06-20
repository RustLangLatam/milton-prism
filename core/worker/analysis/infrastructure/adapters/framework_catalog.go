package adapters

import "strings"

// frameworkEntry is the canonical descriptor for a well-known framework.
type frameworkEntry struct {
	Slug        string // stable lowercase machine identifier (e.g. "laravel")
	DisplayName string // human-readable display label (e.g. "Laravel")
}

// pkgCatalog is the SINGLE SOURCE OF TRUTH for framework slug and display-name
// mapping. Key = lowercase package/gem/module name as it appears in the manifest.
// All parsers and the filesystem detector derive their framework metadata here;
// no separate per-ecosystem framework maps are maintained elsewhere.
var pkgCatalog = map[string]frameworkEntry{
	// PHP / Composer
	"laravel/framework":        {"laravel", "Laravel"},
	"symfony/symfony":          {"symfony", "Symfony"},
	"symfony/framework-bundle": {"symfony", "Symfony"},
	"codeigniter4/framework":   {"codeigniter", "CodeIgniter"},
	"codeigniter/framework":    {"codeigniter", "CodeIgniter"},
	"yiisoft/yii2":             {"yii", "Yii"},
	"cakephp/cakephp":          {"cakephp", "CakePHP"},
	"slim/slim":                {"slim", "Slim"},
	"laminas/laminas-mvc":      {"laminas", "Laminas"},
	// Python / PyPI
	"django":              {"django", "Django"},
	"flask":               {"flask", "Flask"},
	"fastapi":             {"fastapi", "FastAPI"},
	"tornado":             {"tornado", "Tornado"},
	"falcon":              {"falcon", "Falcon"},
	"pyramid":             {"pyramid", "Pyramid"},
	"bottle":              {"bottle", "Bottle"},
	"sanic":               {"sanic", "Sanic"},
	"aiohttp":             {"aiohttp", "aiohttp"},
	"starlette":           {"starlette", "Starlette"},
	"litestar":            {"litestar", "Litestar"},
	"djangorestframework": {"drf", "Django REST Framework"},
	"flask-restful":       {"flask-restful", "Flask-RESTful"},
	// JavaScript / npm
	"express":  {"express", "Express"},
	"fastify":  {"fastify", "Fastify"},
	"koa":      {"koa", "Koa"},
	"next":     {"nextjs", "Next.js"},
	"nuxt":     {"nuxt", "Nuxt"},
	"gatsby":   {"gatsby", "Gatsby"},
	"remix":    {"remix", "Remix"},
	"react":    {"react", "React"},
	"vue":      {"vue", "Vue"},
	"svelte":   {"svelte", "Svelte"},
	"angular":  {"angular", "Angular"},
	"sails":    {"sails", "Sails"},
	"loopback": {"loopback", "LoopBack"},
	// Ruby / RubyGems
	"rails":   {"rails", "Rails"},
	"sinatra": {"sinatra", "Sinatra"},
	"hanami":  {"hanami", "Hanami"},
	"roda":    {"roda", "Roda"},
	"grape":   {"grape", "Grape"},
	"padrino": {"padrino", "Padrino"},
	"camping": {"camping", "Camping"},
}

// npmScopeCatalog maps @scope/ prefixes to framework entries.
// Evaluated before pkgCatalog for scoped npm packages.
var npmScopeCatalog = []struct {
	Prefix string
	Entry  frameworkEntry
}{
	{"@angular/", frameworkEntry{"angular", "Angular"}},
	{"@nestjs/", frameworkEntry{"nestjs", "NestJS"}},
	{"@vue/", frameworkEntry{"vue", "Vue"}},
	{"@remix-run/", frameworkEntry{"remix", "Remix"}},
	{"@sveltejs/", frameworkEntry{"svelte", "Svelte"}},
}

// mavenSpringGroups maps Maven Spring groupIDs to the Spring framework entry.
var mavenSpringGroups = map[string]frameworkEntry{
	"org.springframework.boot":     {Slug: "spring", DisplayName: "Spring"},
	"org.springframework":          {Slug: "spring", DisplayName: "Spring"},
	"org.springframework.security": {Slug: "spring", DisplayName: "Spring"},
	"org.springframework.data":     {Slug: "spring", DisplayName: "Spring"},
	"org.springframework.batch":    {Slug: "spring", DisplayName: "Spring"},
	"org.springframework.cloud":    {Slug: "spring", DisplayName: "Spring"},
}

// displayCatalog maps canonical display names to their slug.
// Used by FileSystemFrameworkDetector which knows the display name at detection time.
var displayCatalog = map[string]string{
	"Laravel":              "laravel",
	"Symfony":              "symfony",
	"CodeIgniter":          "codeigniter",
	"Yii":                  "yii",
	"CakePHP":              "cakephp",
	"Slim":                 "slim",
	"Laminas":              "laminas",
	"Django":               "django",
	"Flask":                "flask",
	"FastAPI":              "fastapi",
	"Tornado":              "tornado",
	"Falcon":               "falcon",
	"Pyramid":              "pyramid",
	"Bottle":               "bottle",
	"Sanic":                "sanic",
	"aiohttp":              "aiohttp",
	"Starlette":            "starlette",
	"Litestar":             "litestar",
	"Django REST Framework": "drf",
	"Flask-RESTful":        "flask-restful",
	"Express":              "express",
	"Fastify":              "fastify",
	"Koa":                  "koa",
	"Next.js":              "nextjs",
	"Nuxt":                 "nuxt",
	"Gatsby":               "gatsby",
	"Remix":                "remix",
	"React":                "react",
	"Vue":                  "vue",
	"Svelte":               "svelte",
	"Angular":              "angular",
	"NestJS":               "nestjs",
	"Sails":                "sails",
	"LoopBack":             "loopback",
	"Rails":                "rails",
	"Sinatra":              "sinatra",
	"Hanami":               "hanami",
	"Roda":                 "roda",
	"Grape":                "grape",
	"Padrino":              "padrino",
	"Camping":              "camping",
	"Spring":               "spring",
}

// frameworkEntryForPkg returns the framework catalog entry for a package name.
// Case-insensitive. For npm scoped packages (@scope/pkg) the scope prefix
// determines the entry. Returns zero value and false when not in the catalog.
func frameworkEntryForPkg(pkgName string) (frameworkEntry, bool) {
	lower := strings.ToLower(pkgName)
	if e, ok := pkgCatalog[lower]; ok {
		return e, true
	}
	for _, s := range npmScopeCatalog {
		if strings.HasPrefix(lower, s.Prefix) {
			return s.Entry, true
		}
	}
	return frameworkEntry{}, false
}

// frameworkEntryForMaven returns the framework catalog entry for a Maven groupID.
func frameworkEntryForMaven(groupID string) (frameworkEntry, bool) {
	e, ok := mavenSpringGroups[groupID]
	return e, ok
}

// frameworkSlugForDisplay returns the slug for a canonical display name.
// Used by FileSystemFrameworkDetector to populate the Slug field.
func frameworkSlugForDisplay(displayName string) string {
	return displayCatalog[displayName]
}
