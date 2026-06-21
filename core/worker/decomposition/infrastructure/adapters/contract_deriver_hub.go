package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.ContractDeriver = (*ContractDeriverHub)(nil)

// ErrContractDeriverNotImplemented is returned for frameworks without a live
// ContractDeriver adapter (non-Flask/SQLAlchemy, non-Laravel/Eloquent stacks in v1).
var ErrContractDeriverNotImplemented = errors.New("contract deriver: not implemented for this framework (v1 supports Flask/SQLAlchemy and Laravel/Eloquent)")

// ContractDeriverHub detects the framework used in the workspace and dispatches
// to the appropriate ContractDeriver adapter. In v1 Flask/SQLAlchemy and
// Laravel/Eloquent are the live adapters; all other frameworks return
// ErrContractDeriverNotImplemented.
type ContractDeriverHub struct {
	flaskAdapter    *FlaskSQLAlchemyDeriver
	eloquentAdapter *EloquentDeriver
}

// NewContractDeriverHub returns a ContractDeriverHub with the Flask/SQLAlchemy
// and Laravel/Eloquent adapters wired.
func NewContractDeriverHub() *ContractDeriverHub {
	return &ContractDeriverHub{
		flaskAdapter:    NewFlaskSQLAlchemyDeriver(),
		eloquentAdapter: NewEloquentDeriver(),
	}
}

// Derive detects the framework from the workspace and delegates accordingly.
// The Flask/SQLAlchemy path is checked first and is byte-identical to its prior
// behaviour; Laravel is only consulted when the workspace is not Flask.
func (h *ContractDeriverHub) Derive(
	ctx context.Context,
	cluster workerdomain.Cluster,
	workspacePath string,
	tableServiceMap map[string]string,
) (*workerdomain.DerivedContract, error) {
	if isFlaskWorkspace(workspacePath) {
		return h.flaskAdapter.Derive(ctx, cluster, workspacePath, tableServiceMap)
	}
	if isLaravelWorkspace(workspacePath) {
		return h.eloquentAdapter.Derive(ctx, cluster, workspacePath, tableServiceMap)
	}
	applog.Warningf("decomposition-worker: contract deriver: no adapter for workspace %s (not Flask/SQLAlchemy, not Laravel/Eloquent) — skipping cluster %s",
		workspacePath, cluster.BlueprintGroup)
	return nil, ErrContractDeriverNotImplemented
}

// isLaravelWorkspace returns true when the workspace looks like a Laravel
// project. Heuristic: composer.json declares laravel/framework, or an `artisan`
// console script sits at the workspace root.
func isLaravelWorkspace(workspacePath string) bool {
	if data, err := os.ReadFile(filepath.Join(workspacePath, "composer.json")); err == nil {
		if strings.Contains(string(data), "laravel/framework") {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "artisan")); err == nil {
		return true
	}
	return false
}

// isFlaskWorkspace returns true when the workspace looks like a Flask/SQLAlchemy project.
// Heuristic: presence of Flask in any Python dependency file, or any .py file that
// imports Flask.
func isFlaskWorkspace(workspacePath string) bool {
	// Check Pipfile.
	if containsFlaskDep(workspacePath, "Pipfile") {
		return true
	}
	// Check requirements.txt variants.
	for _, req := range []string{"requirements.txt", "requirements/base.txt", "requirements/prod.txt"} {
		if containsFlaskDep(workspacePath, req) {
			return true
		}
	}
	// Check for any .py file that imports Flask.
	found := false
	_ = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if found || err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".py") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), "from flask") || strings.Contains(string(data), "import flask") {
			found = true
		}
		return nil
	})
	return found
}

// containsFlaskDep reads a file relative to workspacePath and checks for the string "flask".
func containsFlaskDep(workspacePath, relPath string) bool {
	data, err := os.ReadFile(filepath.Join(workspacePath, relPath))
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "flask")
}
