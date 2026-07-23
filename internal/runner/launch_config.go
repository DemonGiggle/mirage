package runner

import "strconv"

// backendLaunchConfig is the complete contract passed to __backend-exec.
// Keeping serialization here makes the process boundary explicit and testable.
type backendLaunchConfig struct {
	Self             string
	RootFS           string
	Cwd              string
	Hostname         string
	NetworkBackend   string
	SerializedPolicy string
	RoutedInterface  string
	RoutedAddress    string
	RoutedGateway    string
	NetworkReadyFD   int
	ROBind           []string
	RWBind           []string
	Env              []string
	RunAsRoot        bool
	Command          []string
}

func (cfg backendLaunchConfig) args() []string {
	args := []string{cfg.Self, "__backend-exec", "--rootfs", cfg.RootFS, "--network-backend", cfg.NetworkBackend}
	if cfg.SerializedPolicy != "" {
		args = append(args, "--policy-config", cfg.SerializedPolicy)
	}
	if cfg.RoutedInterface != "" {
		args = append(args,
			"--routed-interface", cfg.RoutedInterface,
			"--routed-address", cfg.RoutedAddress,
			"--routed-gateway", cfg.RoutedGateway,
			"--network-ready-fd", strconv.Itoa(cfg.NetworkReadyFD),
		)
	}
	if cfg.Cwd != "" {
		args = append(args, "--cwd", cfg.Cwd)
	}
	if cfg.Hostname != "" {
		args = append(args, "--hostname", cfg.Hostname)
	}
	for _, item := range cfg.ROBind {
		args = append(args, "--ro-bind", item)
	}
	for _, item := range cfg.RWBind {
		args = append(args, "--rw-bind", item)
	}
	for _, item := range cfg.Env {
		args = append(args, "--env", item)
	}
	if cfg.RunAsRoot {
		args = append(args, "--run-as-root")
	}
	args = append(args, "--")
	return append(args, cfg.Command...)
}
