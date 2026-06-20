// Package phpclassify provides shared PHP namespace-segment classification
// logic used by both the analysis and decomposition workers.
//
// This is the single source of truth for PHP layer rules (Laravel, Symfony,
// CodeIgniter 4, and idiomatic PHP naming). Both workers import this package
// so that the segment rules are never duplicated.
package phpclassify

import "strings"

// IsPHPModule reports whether name uses PHP's backslash namespace separator.
func IsPHPModule(name string) bool {
	return strings.Contains(name, `\`)
}

// LayerOf returns the architectural layer for a PHP FQN.
//
// Priority: test > application > infra > domain.
// Returns "" when no namespace segment matches a known pattern — the caller
// should apply a structural fan-in fallback for those modules.
func LayerOf(fqn string) string {
	segs := strings.Split(fqn, `\`)

	// Test detection: namespace-prefix form (Tests\..., Test\...) and
	// class-name-suffix form (...FooTest, ...FooTests, ...FooTestCase).
	if len(segs) > 0 && (segs[0] == "Tests" || segs[0] == "Test") {
		return "test"
	}
	last := segs[len(segs)-1]
	if strings.HasSuffix(last, "Test") || strings.HasSuffix(last, "Tests") || strings.HasSuffix(last, "TestCase") {
		return "test"
	}

	// Walk segments: application is returned immediately (highest priority);
	// infra beats domain; domain beats unmatched.
	best := ""
	for _, seg := range segs {
		layer := segmentLayer(seg)
		if layer == "" {
			continue
		}
		if layer == "application" {
			return "application"
		}
		if layer == "infra" && best != "infra" {
			best = "infra"
		}
		if layer == "domain" && best == "" {
			best = "domain"
		}
	}
	return best
}

// segmentLayer maps a single namespace segment name to its architectural layer.
// Returns "" for segments that carry no layer signal.
func segmentLayer(seg string) string {
	switch seg {
	// APPLICATION — HTTP controllers, CLI commands, middleware, form requests.
	case "Controllers", "Controller",
		"Console", "Commands", "Command",
		"Middleware",
		"Requests", "Request", "FormRequests",
		"Actions", "Action":
		return "application"

	// INFRA — persistence, events, messaging, service-locator, dev tooling,
	// and exception classes. Exception modules are error contracts owned by
	// their callers and do not form service boundaries — classifying them as
	// infra keeps them out of the domain clustering subgraph in both the
	// analysis and decomposition workers.
	case "Repositories", "Repository", "Repos", "Repo",
		"Providers", "Provider",
		"Facades", "Facade",
		"Events", "Event",
		"Listeners", "Listener",
		"Jobs", "Job",
		"Notifications", "Notification",
		"Mail",
		"Adapters", "Adapter",
		"Infrastructure",
		"DependencyInjection",
		"Queries", "Query",
		"Observers", "Observer",
		"Migrations", "Migration",
		"Seeders", "Seeder",
		"Factories", "Factory",
		"Casts", "Cast",
		"Broadcast", "Broadcasting",
		"Uploads", "Upload",
		"Exceptions", "Exception":
		return "infra"

	// DOMAIN — business entities, rules, services, contracts.
	case "Models", "Model",
		"Entities", "Entity",
		"Services", "Service",
		"Policies", "Policy",
		"Rules", "Rule",
		"Contracts", "Contract",
		"Permissions", "Permission",
		"Tools", "Tool",
		"Builders", "Builder",
		"ValueObjects", "ValueObject",
		"Enums", "Enum",
		"Domain",
		"Access":
		return "domain"
	}
	return ""
}
