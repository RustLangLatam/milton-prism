package agent_test

import (
	"testing"
	"time"

	"milton_prism/core/worker/generation/infrastructure/agent"
)

// TestResourceTierFor proves the #6e tier selector: rust, java, csharp and cpp
// resolve to the heavy tier (4 GiB / 2 CPU / 90 min), while go/python/node/ruby/
// unknown/empty fall back to the safe default tier (1 GiB / 50% CPU / 60 min). This
// replaces the old `if == "rust"` branch and confirms Java is now actually wired to
// 4 GiB (the audit found the Java commit claimed 4 GiB but never cabled it), plus
// csharp (Roslyn / dotnet build peaks >1 GiB) and cpp (g++/CMake link of grpc++/
// mongocxx peaks >1 GiB).
func TestResourceTierFor(t *testing.T) {
	heavy := []string{"rust", "java", "csharp", "cpp", "RUST", "Java", "CSharp", " cpp "}
	for _, p := range heavy {
		cpu, mem, timeout, isHeavy := agent.ResourceTierFor(p)
		if !isHeavy {
			t.Errorf("profile %q: want heavy tier, got default", p)
		}
		if cpu != agent.HeavyAgentCPUQuota {
			t.Errorf("profile %q: cpuQuota=%d, want %d", p, cpu, agent.HeavyAgentCPUQuota)
		}
		if mem != agent.HeavyAgentMemory {
			t.Errorf("profile %q: memoryBytes=%d, want %d (4 GiB)", p, mem, agent.HeavyAgentMemory)
		}
		if timeout != agent.HeavyAgentTimeout {
			t.Errorf("profile %q: timeout=%s, want %s", p, timeout, agent.HeavyAgentTimeout)
		}
	}

	defaults := []string{"go", "python", "node", "ruby", "", "erlang", "haskell"}
	for _, p := range defaults {
		cpu, mem, timeout, isHeavy := agent.ResourceTierFor(p)
		if isHeavy {
			t.Errorf("profile %q: want default tier, got heavy", p)
		}
		if cpu != agent.DefaultAgentCPUQuota {
			t.Errorf("profile %q: cpuQuota=%d, want %d", p, cpu, agent.DefaultAgentCPUQuota)
		}
		if mem != agent.DefaultAgentMemory {
			t.Errorf("profile %q: memoryBytes=%d, want %d (1 GiB)", p, mem, agent.DefaultAgentMemory)
		}
		if timeout != agent.DefaultAgentTimeout {
			t.Errorf("profile %q: timeout=%s, want %s", p, timeout, agent.DefaultAgentTimeout)
		}
	}
}

// TestHeavyTierValues pins the absolute heavy-tier numbers so an accidental edit
// (e.g. dropping Java back to 1 GiB) trips the test.
func TestHeavyTierValues(t *testing.T) {
	if agent.HeavyAgentMemory != int64(4)<<30 {
		t.Errorf("heavy memory=%d, want 4 GiB", agent.HeavyAgentMemory)
	}
	if agent.HeavyAgentCPUQuota != 200_000 {
		t.Errorf("heavy cpuQuota=%d, want 200000 (2 CPU)", agent.HeavyAgentCPUQuota)
	}
	if agent.HeavyAgentTimeout != 90*time.Minute {
		t.Errorf("heavy timeout=%s, want 90m", agent.HeavyAgentTimeout)
	}
	if agent.DefaultAgentMemory != int64(1)<<30 {
		t.Errorf("default memory=%d, want 1 GiB", agent.DefaultAgentMemory)
	}
	if agent.DefaultAgentTimeout != 60*time.Minute {
		t.Errorf("default timeout=%s, want 60m", agent.DefaultAgentTimeout)
	}
}
