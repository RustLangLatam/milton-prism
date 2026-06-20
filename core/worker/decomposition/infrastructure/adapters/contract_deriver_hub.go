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
// ContractDeriver adapter (non-Flask/SQLAlchemy stacks in v1).
var ErrContractDeriverNotImplemented = errors.New("contract deriver: not implemented for this framework (v1 supports Flask/SQLAlchemy only)")

// ContractDeriverHub detects the framework used in the workspace and dispatches
// to the appropriate ContractDeriver adapter. In v1 Flask/SQLAlchemy is the
// only live adapter; all other frameworks return ErrContractDeriverNotImplemented.
type ContractDeriverHub struct {
	flaskAdapter *FlaskSQLAlchemyDeriver
}

// NewContractDeriverHub returns a ContractDeriverHub with the Flask/SQLAlchemy adapter wired.
func NewContractDeriverHub() *ContractDeriverHub {
	return &ContractDeriverHub{flaskAdapter: NewFlaskSQLAlchemyDeriver()}
}

// Derive detects the framework from the workspace and delegates accordingly.
func (h *ContractDeriverHub) Derive(
	ctx context.Context,
	cluster workerdomain.Cluster,
	workspacePath string,
	tableServiceMap map[string]string,
) (*workerdomain.DerivedContract, error) {
	if isFlaskWorkspace(workspacePath) {
		return h.flaskAdapter.Derive(ctx, cluster, workspacePath, tableServiceMap)
	}
	applog.Warningf("decomposition-worker: contract deriver: no adapter for workspace %s (not Flask/SQLAlchemy) — skipping cluster %s",
		workspacePath, cluster.BlueprintGroup)
	return nil, ErrContractDeriverNotImplemented
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
