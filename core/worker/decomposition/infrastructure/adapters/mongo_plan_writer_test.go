package adapters

import (
	"strings"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// TestBuildBoundarySpecYAML_Articles verifies the YAML spec for the articles service:
// shared_database=true, cross-service FKs listed with service resolution.
// The function under test is now workerdomain.BuildBoundarySpecYAML (domain layer).
func TestBuildBoundarySpecYAML_Articles(t *testing.T) {
	svc := &workerdomain.ProposedService{
		Name:             "articles",
		ErrorPrefix:      "ART",
		OwnedResources:   []string{"conduit.articles.models"},
		InterServiceDeps: []string{"profile", "user"},
	}
	crossFKs := []workerdomain.CrossServiceFK{
		{OwnerService: "articles", OwnerMessage: "Article", FieldName: "author_identifier", RefTable: "userprofile", RefService: "profile"},
	}
	opCouplings := []workerdomain.OperationalCoupling{
		{FromService: "articles", ToService: "user", FromModule: "conduit.articles.views"},
	}

	yaml := workerdomain.BuildBoundarySpecYAML(svc, true, crossFKs, opCouplings)
	t.Logf("\n--- articles boundary spec ---\n%s--- end ---", yaml)

	checks := []struct{ want, desc string }{
		{"name: articles", "service name"},
		{"error_prefix: ART", "error prefix"},
		{"owned_resources:", "owned_resources section"},
		{"conduit.articles.models", "owned resource entry"},
		{"inter_service_deps:", "inter_service_deps section"},
		{"- profile", "profile dep"},
		{"shared_database: true", "shared_database flag"},
		{"cross_service_fks:", "cross_service_fks section"},
		{"owner_message: Article", "FK owner message"},
		{"field: author_identifier", "FK field name"},
		{"ref_table: userprofile", "FK ref table"},
		{"ref_service: profile", "FK ref service"},
		{"operational_couplings:", "operational couplings section"},
		{"conduit.articles.views", "operational coupling source module"},
		{"TODO: per-service data ownership", "ownership TODO marker"},
	}
	for _, c := range checks {
		if !strings.Contains(yaml, c.want) {
			t.Errorf("boundary spec missing %s: want %q", c.desc, c.want)
		}
	}
}

// TestBuildBoundarySpecYAML_User verifies the YAML spec for the user service:
// no cross-service FKs, no inter_service_deps.
func TestBuildBoundarySpecYAML_User(t *testing.T) {
	svc := &workerdomain.ProposedService{
		Name:             "user",
		ErrorPrefix:      "USE",
		OwnedResources:   []string{"conduit.user.models"},
		InterServiceDeps: nil,
	}

	yaml := workerdomain.BuildBoundarySpecYAML(svc, true, nil, nil)

	if !strings.Contains(yaml, "name: user") {
		t.Error("missing name: user")
	}
	if !strings.Contains(yaml, "error_prefix: USE") {
		t.Error("missing error_prefix: USE")
	}
	if !strings.Contains(yaml, "shared_database: true") {
		t.Error("missing shared_database: true")
	}
	if strings.Contains(yaml, "cross_service_fks:") {
		t.Error("should not have cross_service_fks when none exist")
	}
}
