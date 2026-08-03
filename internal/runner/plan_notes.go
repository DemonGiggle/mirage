package runner

import (
	"fmt"
	"strings"

	"github.com/DemonGiggle/mirage/internal/spec"
)

// PlanNotes describes the concrete backend choices made for a resolved config.
func PlanNotes(cfg spec.Config) []string {
	notes := []string{
		"execution backend: linux namespace runner",
		"execution mode: direct workload command becomes sandbox PID 1",
		"one sandbox = one isolated process tree",
	}
	if cfg.NetworkPolicy != nil {
		if plan, err := planNetworkPolicyBackend(cfg); err == nil {
			switch plan.BackendMode {
			case backendNetworkPolicyHost:
				notes = append(notes, "network backend: allow-all policy via host namespace passthrough")
			case backendNetworkPolicyIsolated:
				notes = append(notes, fmt.Sprintf("network backend: isolated policy namespace (%s loopback)", plan.LoopbackAction))
			case backendNetworkPolicyRouted:
				notes = append(notes, fmt.Sprintf("network backend: routed policy namespace (%s loopback, host NAT uplink)", plan.LoopbackAction))
			}
		} else {
			notes = append(notes, fmt.Sprintf("network backend: networkPolicy unsupported by current backend (%v)", err))
		}
	}
	if cfg.StdoutLog != "" || cfg.StderrLog != "" {
		var exports []string
		if cfg.StdoutLog != "" {
			exports = append(exports, "stdout")
		}
		if cfg.StderrLog != "" {
			exports = append(exports, "stderr")
		}
		notes = append(notes, fmt.Sprintf("host log export: %s", strings.Join(exports, "+")))
	}
	if len(cfg.ROBind) > 0 || len(cfg.RWBind) > 0 {
		notes = append(notes, "bind mounts: enforced read-only/read-write host path exposure")
	}
	if cfg.Memory != "" || cfg.Pids > 0 {
		var limits []string
		if cfg.Memory != "" {
			limits = append(limits, "memory="+cfg.Memory)
		}
		if cfg.Pids > 0 {
			limits = append(limits, fmt.Sprintf("pids=%d", cfg.Pids))
		}
		notes = append(notes, fmt.Sprintf("cgroup v2: enforced via delegated systemd scope leaf cgroup (%s)", strings.Join(limits, ", ")))
	}
	if cfg.ScopeName != "" {
		notes = append(notes, fmt.Sprintf("systemd scope: %s", cfg.ScopeName))
	}
	if cfg.RootFS == "/" {
		notes = append(notes, "rootfs backend: host root")
	} else {
		notes = append(notes, "rootfs backend: mounted runtime layout plus chroot handoff")
	}
	if cfg.RunAsRoot {
		notes = append(notes, "workload identity: root (explicit via --run-as-root)")
	} else {
		notes = append(notes, fmt.Sprintf("workload identity: non-root %s (%d:%d)", defaultSandboxUser, sandboxUID, sandboxGID))
	}
	if cfg.EnableSudo {
		notes = append(notes, "guest sudo: passwordless escalation to namespace root enabled")
	}
	return notes
}
