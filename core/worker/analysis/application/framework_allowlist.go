package application

import "strings"

// isFrameworkEntrypoint reports whether a statically-unreachable module is a known
// framework entry point — a class the runtime framework reaches by a mechanism the
// static import extractor cannot see (kernel arrays, the service container, route
// middleware aliases, console auto-discovery, factory/seeder conventions). Such
// modules are real production code that merely appears as a graph island; they are
// suppressed from the "statically unreachable — review" report so it carries signal
// rather than framework noise.
//
// The allowlist NEVER affects the production count (every has-code module counts
// regardless) and degrades safely: a pattern it does not recognise returns false,
// so the module stays in the review report — it is never silently dropped and the
// report never tells a user to delete a reachable class.
//
// Language-aware by construction: only PHP/Laravel is covered today (PHP modules
// use backslash namespaces). Other languages have no entries and fall through to
// false; Python's real islands are empty package markers, already filtered upstream.
func isFrameworkEntrypoint(module string) bool {
	// Non-PHP modules (no backslash namespace separator) have no allowlist yet.
	if !strings.Contains(module, `\`) {
		return false
	}

	// Laravel entry points, scoped by namespace path so homonyms outside the
	// framework location (e.g. a helper named *Factory outside Database\Factories)
	// are not falsely suppressed.
	switch {
	case strings.Contains(module, `\Http\Middleware\`):
		return true
	case strings.HasSuffix(module, `\Http\Kernel`), strings.HasSuffix(module, `\Console\Kernel`):
		return true
	case strings.Contains(module, `\Console\Commands\`):
		return true
	case strings.Contains(module, `\Providers\`) || strings.HasSuffix(module, `ServiceProvider`):
		return true
	case strings.Contains(module, `Database\Factories\`):
		return true
	case strings.Contains(module, `Database\Seeders\`):
		return true
	case strings.HasSuffix(module, `\App\Application`):
		return true
	case strings.HasSuffix(module, `Interface`):
		// Marker/contract interfaces are reached via `implements`, an edge the
		// extractor does not currently resolve.
		return true
	}
	return false
}
