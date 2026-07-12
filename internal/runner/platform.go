package runner

import (
	"os"
	"syscall"
)

// These replaceable operations keep privileged OS interactions explicit and
// allow focused tests without entering namespaces or changing process IDs.
var (
	currentUID         = os.Getuid
	currentGID         = os.Getgid
	currentGroups      = os.Getgroups
	chownFunc          = os.Lchown
	idMapCommandRunner = runIDMapCommand
	procfsRoot         = "/proc"
	setgroupsFunc      = syscall.Setgroups
	setgidFunc         = syscall.Setgid
	setuidFunc         = syscall.Setuid
)
