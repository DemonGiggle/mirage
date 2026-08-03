package hostenv

// Kind describes the privilege level of the host process before Mirage enters
// any user namespace. Keeping this distinct from the guest identity avoids
// conflating namespace root with host root.
type Kind string

const (
	Root     Kind = "root"
	Rootless Kind = "rootless"
)

// Detect classifies a host effective user ID.
func Detect(euid int) Kind {
	if euid == 0 {
		return Root
	}
	return Rootless
}

func (kind Kind) IsRoot() bool {
	return kind == Root
}
