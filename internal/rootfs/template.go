package rootfs

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const TemplateVersionV1 = "v1"

type Template struct {
	Version        string          `json:"version"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Directories    []Directory     `json:"directories,omitempty"`
	Binaries       []Binary        `json:"binaries,omitempty"`
	RuntimeTrees   []RuntimeTree   `json:"runtime_trees,omitempty"`
	RuntimeFiles   []RuntimeFile   `json:"runtime_files,omitempty"`
	GeneratedFiles []GeneratedFile `json:"generated_files,omitempty"`
}

type Directory struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode,omitempty"`
}

type Binary struct {
	TargetPath       string `json:"target_path"`
	HostPath         string `json:"host_path,omitempty"`
	LookupName       string `json:"lookup_name,omitempty"`
	CopyDependencies bool   `json:"copy_dependencies,omitempty"`
}

type RuntimeTree struct {
	HostPath   string `json:"host_path"`
	TargetPath string `json:"target_path"`
	Optional   bool   `json:"optional,omitempty"`
}

type RuntimeFile struct {
	HostPath   string `json:"host_path"`
	TargetPath string `json:"target_path"`
	Optional   bool   `json:"optional,omitempty"`
}

type GeneratedFile struct {
	TargetPath string `json:"target_path"`
	Content    string `json:"content,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
}

var BuiltInTemplates = map[string]Template{
	"basic":              basicTemplate(),
	"node":               nodeTemplate(),
	"python":             pythonTemplate(),
	"openclaw":           openclawTemplate(),
	"openclaw-chat-only": openclawChatOnlyTemplate(),
	"openclaw-work":      openclawWorkTemplate(),
	"openclaw-developer": openclawDeveloperTemplate(),
	"openclaw-admin":     openclawAdminTemplate(),
	"openclaw-root":      openclawRootTemplate(),
	"openclaw-systemd":   openclawSystemdTemplate(),
}

func TemplateNames() []string {
	names := make([]string, 0, len(BuiltInTemplates))
	for name := range BuiltInTemplates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func LookupTemplate(name string) (Template, bool) {
	template, ok := BuiltInTemplates[name]
	if !ok {
		return Template{}, false
	}
	return cloneTemplate(template), true
}

func AvailableTemplates() map[string]Template {
	templates := make(map[string]Template, len(BuiltInTemplates))
	for name, template := range BuiltInTemplates {
		templates[name] = cloneTemplate(template)
	}
	return templates
}

func ValidateTemplate(template Template) error {
	var problems []error
	seenDirectories := make(map[string]struct{}, len(template.Directories))
	seenBinaries := make(map[string]struct{}, len(template.Binaries))
	seenPaths := make(map[string]struct{}, len(template.RuntimeTrees)+len(template.RuntimeFiles)+len(template.GeneratedFiles))
	if template.Version != TemplateVersionV1 {
		problems = append(problems, fmt.Errorf("template %q must declare version %q", template.Name, TemplateVersionV1))
	}
	if strings.TrimSpace(template.Name) == "" {
		problems = append(problems, errors.New("template name is required"))
	}
	if strings.TrimSpace(template.Description) == "" {
		problems = append(problems, fmt.Errorf("template %q description is required", template.Name))
	}
	for idx, dir := range template.Directories {
		if !filepath.IsAbs(dir.Path) {
			problems = append(problems, fmt.Errorf("template %q directory %d path %q must be absolute", template.Name, idx, dir.Path))
		}
		if _, exists := seenDirectories[dir.Path]; exists {
			problems = append(problems, fmt.Errorf("template %q directory path %q is duplicated", template.Name, dir.Path))
			continue
		}
		seenDirectories[dir.Path] = struct{}{}
	}
	for idx, binary := range template.Binaries {
		if !filepath.IsAbs(binary.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q binary %d target path %q must be absolute", template.Name, idx, binary.TargetPath))
		}
		if _, exists := seenBinaries[binary.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q binary target path %q is duplicated", template.Name, binary.TargetPath))
			continue
		}
		seenBinaries[binary.TargetPath] = struct{}{}
		hasHostPath := binary.HostPath != ""
		hasLookup := binary.LookupName != ""
		switch {
		case hasHostPath == hasLookup:
			problems = append(problems, fmt.Errorf("template %q binary %d must set exactly one of host_path or lookup_name", template.Name, idx))
		case hasHostPath && !filepath.IsAbs(binary.HostPath):
			problems = append(problems, fmt.Errorf("template %q binary %d host path %q must be absolute", template.Name, idx, binary.HostPath))
		case hasLookup && strings.ContainsRune(binary.LookupName, filepath.Separator):
			problems = append(problems, fmt.Errorf("template %q binary %d lookup name %q must be a PATH name, not a path", template.Name, idx, binary.LookupName))
		}
	}
	for idx, runtimeTree := range template.RuntimeTrees {
		if !filepath.IsAbs(runtimeTree.HostPath) {
			problems = append(problems, fmt.Errorf("template %q runtime tree %d host path %q must be absolute", template.Name, idx, runtimeTree.HostPath))
		}
		if !filepath.IsAbs(runtimeTree.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q runtime tree %d target path %q must be absolute", template.Name, idx, runtimeTree.TargetPath))
		}
		if _, exists := seenPaths[runtimeTree.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q runtime tree target path %q is duplicated", template.Name, runtimeTree.TargetPath))
			continue
		}
		seenPaths[runtimeTree.TargetPath] = struct{}{}
	}
	for idx, runtimeFile := range template.RuntimeFiles {
		if !filepath.IsAbs(runtimeFile.HostPath) {
			problems = append(problems, fmt.Errorf("template %q runtime file %d host path %q must be absolute", template.Name, idx, runtimeFile.HostPath))
		}
		if !filepath.IsAbs(runtimeFile.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q runtime file %d target path %q must be absolute", template.Name, idx, runtimeFile.TargetPath))
		}
		if _, exists := seenPaths[runtimeFile.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q runtime file target path %q is duplicated", template.Name, runtimeFile.TargetPath))
			continue
		}
		seenPaths[runtimeFile.TargetPath] = struct{}{}
	}
	for idx, generatedFile := range template.GeneratedFiles {
		if !filepath.IsAbs(generatedFile.TargetPath) {
			problems = append(problems, fmt.Errorf("template %q generated file %d target path %q must be absolute", template.Name, idx, generatedFile.TargetPath))
		}
		if _, exists := seenPaths[generatedFile.TargetPath]; exists {
			problems = append(problems, fmt.Errorf("template %q generated file target path %q is duplicated", template.Name, generatedFile.TargetPath))
			continue
		}
		seenPaths[generatedFile.TargetPath] = struct{}{}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.Join(problems...)
}

func basicTemplate() Template {
	return Template{
		Version:     TemplateVersionV1,
		Name:        "basic",
		Description: "Small runnable base rootfs with shell and core inspection tools.",
		Directories: append([]Directory{}, commonRuntimeDirectories...),
		Binaries: []Binary{
			lookupBinary("sh", "/bin/sh"),
			lookupBinary("ls", "/bin/ls"),
			lookupBinary("cat", "/bin/cat"),
			lookupBinary("mkdir", "/bin/mkdir"),
			lookupBinary("pwd", "/bin/pwd"),
			lookupBinary("rm", "/bin/rm"),
			lookupBinary("true", "/bin/true"),
			lookupBinary("false", "/bin/false"),
			lookupBinary("env", "/usr/bin/env"),
		},
		RuntimeFiles: append([]RuntimeFile{}, commonRuntimeFiles...),
	}
}

func nodeTemplate() Template {
	template := basicTemplate()
	template.Name = "node"
	template.Description = "Node.js-oriented rootfs template with npm and HTTPS trust material."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/workspace", 0o755),
		directory("/etc/ssl/certs", 0o755),
	)
	template.Binaries = append(template.Binaries,
		lookupBinary("node", "/usr/bin/node"),
		lookupBinary("npm", "/usr/bin/npm"),
		lookupBinary("npx", "/usr/bin/npx"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		optionalRuntimeFile("/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/certs/ca-certificates.crt"),
		optionalRuntimeFile("/etc/ssl/cert.pem", "/etc/ssl/cert.pem"),
		optionalRuntimeFile("/etc/pki/tls/certs/ca-bundle.crt", "/etc/pki/tls/certs/ca-bundle.crt"),
	)
	return template
}

func pythonTemplate() Template {
	template := basicTemplate()
	template.Name = "python"
	template.Description = "Python-oriented rootfs template with pip and common HTTPS trust material."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/workspace", 0o755),
		directory("/etc/ssl/certs", 0o755),
	)
	template.Binaries = append(template.Binaries,
		lookupBinary("python3", "/usr/bin/python3"),
		lookupBinary("pip3", "/usr/bin/pip3"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		optionalRuntimeFile("/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/certs/ca-certificates.crt"),
		optionalRuntimeFile("/etc/ssl/cert.pem", "/etc/ssl/cert.pem"),
		optionalRuntimeFile("/etc/pki/tls/certs/ca-bundle.crt", "/etc/pki/tls/certs/ca-bundle.crt"),
	)
	return template
}

func openclawTemplate() Template {
	template := nodeTemplate()
	template.Name = "openclaw"
	template.Description = "OpenClaw-oriented compatibility template with Node.js, Git, Bash, and a writable workspace."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/workspace", 0o755),
		directory("/home", 0o755),
	)
	template.Binaries = appendUniqueBinaries(template.Binaries,
		lookupBinary("bash", "/bin/bash"),
		lookupBinary("git", "/usr/bin/git"),
	)
	return template
}

func openclawChatOnlyTemplate() Template {
	template := nodeTemplate()
	template.Name = "openclaw-chat-only"
	template.Description = "OpenClaw chat-only rootfs level with Node.js, TLS material, locales, tzdata, and OpenSSL."
	template.RuntimeTrees = appendUniqueRuntimeTrees(template.RuntimeTrees,
		optionalRuntimeTree("/usr/share/zoneinfo", "/usr/share/zoneinfo"),
		optionalRuntimeTree("/usr/lib/locale", "/usr/lib/locale"),
		optionalRuntimeTree("/usr/share/locale", "/usr/share/locale"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		optionalRuntimeFile("/etc/localtime", "/etc/localtime"),
		optionalRuntimeFile("/etc/timezone", "/etc/timezone"),
		optionalRuntimeFile("/etc/ssl/openssl.cnf", "/etc/ssl/openssl.cnf"),
	)
	template.Binaries = appendUniqueBinaries(template.Binaries,
		lookupBinary("openssl", "/usr/bin/openssl"),
	)
	return template
}

func openclawWorkTemplate() Template {
	template := openclawChatOnlyTemplate()
	template.Name = "openclaw-work"
	template.Description = "OpenClaw work rootfs level with shell, archive, patching, JSON, and search tooling."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/home", 0o755),
	)
	template.RuntimeTrees = appendUniqueRuntimeTrees(template.RuntimeTrees,
		optionalRuntimeTree("/lib/terminfo", "/lib/terminfo"),
		optionalRuntimeTree("/usr/share/terminfo", "/usr/share/terminfo"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		optionalRuntimeFile("/usr/share/misc/magic.mgc", "/usr/share/misc/magic.mgc"),
	)
	template.Binaries = appendUniqueBinaries(template.Binaries,
		lookupBinary("bash", "/bin/bash"),
		lookupBinary("cp", "/bin/cp"),
		lookupBinary("mv", "/bin/mv"),
		lookupBinary("ln", "/bin/ln"),
		lookupBinary("chmod", "/bin/chmod"),
		lookupBinary("chown", "/bin/chown"),
		lookupBinary("touch", "/bin/touch"),
		lookupBinary("sleep", "/bin/sleep"),
		lookupBinary("stat", "/usr/bin/stat"),
		lookupBinary("tee", "/usr/bin/tee"),
		lookupBinary("head", "/usr/bin/head"),
		lookupBinary("tail", "/usr/bin/tail"),
		lookupBinary("sort", "/usr/bin/sort"),
		lookupBinary("uniq", "/usr/bin/uniq"),
		lookupBinary("cut", "/usr/bin/cut"),
		lookupBinary("wc", "/usr/bin/wc"),
		lookupBinary("du", "/usr/bin/du"),
		lookupBinary("df", "/usr/bin/df"),
		lookupBinary("basename", "/usr/bin/basename"),
		lookupBinary("dirname", "/usr/bin/dirname"),
		lookupBinary("readlink", "/usr/bin/readlink"),
		lookupBinary("printf", "/usr/bin/printf"),
		lookupBinary("id", "/usr/bin/id"),
		lookupBinary("whoami", "/usr/bin/whoami"),
		lookupBinary("uname", "/usr/bin/uname"),
		lookupBinary("seq", "/usr/bin/seq"),
		lookupBinary("tr", "/usr/bin/tr"),
		lookupBinary("find", "/usr/bin/find"),
		lookupBinary("xargs", "/usr/bin/xargs"),
		lookupBinary("grep", "/usr/bin/grep"),
		lookupBinary("sed", "/usr/bin/sed"),
		lookupBinary("awk", "/usr/bin/awk"),
		lookupBinary("diff", "/usr/bin/diff"),
		lookupBinary("patch", "/usr/bin/patch"),
		lookupBinary("less", "/usr/bin/less"),
		lookupBinary("file", "/usr/bin/file"),
		lookupBinary("tar", "/usr/bin/tar"),
		lookupBinary("gzip", "/usr/bin/gzip"),
		lookupBinary("bzip2", "/usr/bin/bzip2"),
		lookupBinary("xz", "/usr/bin/xz"),
		lookupBinary("zip", "/usr/bin/zip"),
		lookupBinary("unzip", "/usr/bin/unzip"),
		lookupBinary("jq", "/usr/bin/jq"),
		lookupBinary("rg", "/usr/bin/rg"),
	)
	return template
}

func openclawDeveloperTemplate() Template {
	template := openclawWorkTemplate()
	template.Name = "openclaw-developer"
	template.Description = "OpenClaw developer rootfs level with VCS, editors, interpreters, databases, and common build toolchains."
	template.Binaries = appendUniqueBinaries(template.Binaries,
		lookupBinary("git", "/usr/bin/git"),
		lookupBinary("make", "/usr/bin/make"),
		lookupBinary("ps", "/usr/bin/ps"),
		lookupBinary("pgrep", "/usr/bin/pgrep"),
		lookupBinary("pkill", "/usr/bin/pkill"),
		lookupBinary("curl", "/usr/bin/curl"),
		lookupBinary("wget", "/usr/bin/wget"),
		lookupBinary("fdfind", "/usr/bin/fdfind"),
		lookupBinary("xxd", "/usr/bin/xxd"),
		lookupBinary("vim", "/usr/bin/vim"),
		lookupBinary("python3", "/usr/bin/python3"),
		lookupBinary("pip3", "/usr/bin/pip3"),
		lookupBinary("sqlite3", "/usr/bin/sqlite3"),
		lookupBinary("gcc", "/usr/bin/gcc"),
		lookupBinary("g++", "/usr/bin/g++"),
		lookupBinary("cc", "/usr/bin/cc"),
		lookupBinary("ld", "/usr/bin/ld"),
		lookupBinary("ar", "/usr/bin/ar"),
		lookupBinary("as", "/usr/bin/as"),
		lookupBinary("strip", "/usr/bin/strip"),
		lookupBinary("go", "/usr/bin/go"),
		lookupBinary("rustc", "/usr/bin/rustc"),
		lookupBinary("cargo", "/usr/bin/cargo"),
	)
	template.RuntimeTrees = appendUniqueRuntimeTrees(template.RuntimeTrees,
		optionalRuntimeTree("/usr/lib/python3", "/usr/lib/python3"),
		optionalRuntimeTree("/usr/lib/python3.10", "/usr/lib/python3.10"),
		optionalRuntimeTree("/usr/lib/python3.11", "/usr/lib/python3.11"),
		optionalRuntimeTree("/usr/lib/python3.12", "/usr/lib/python3.12"),
		optionalRuntimeTree("/usr/lib/python3.13", "/usr/lib/python3.13"),
		optionalRuntimeTree("/usr/lib/python3/dist-packages", "/usr/lib/python3/dist-packages"),
		optionalRuntimeTree("/usr/local/lib/python3.10", "/usr/local/lib/python3.10"),
		optionalRuntimeTree("/usr/local/lib/python3.11", "/usr/local/lib/python3.11"),
		optionalRuntimeTree("/usr/local/lib/python3.12", "/usr/local/lib/python3.12"),
		optionalRuntimeTree("/usr/local/lib/python3.13", "/usr/local/lib/python3.13"),
		optionalRuntimeTree("/usr/local/go", "/usr/local/go"),
		optionalRuntimeTree("/usr/lib/go", "/usr/lib/go"),
		optionalRuntimeTree("/usr/lib/rustlib", "/usr/lib/rustlib"),
		optionalRuntimeTree("/usr/lib/cargo", "/usr/lib/cargo"),
	)
	return template
}

func openclawAdminTemplate() Template {
	template := openclawDeveloperTemplate()
	template.Name = "openclaw-admin"
	template.Description = "OpenClaw admin rootfs level with networking, process, capability, and sync utilities."
	template.Binaries = appendUniqueBinaries(template.Binaries,
		lookupBinary("ip", "/usr/sbin/ip"),
		lookupBinary("ss", "/usr/bin/ss"),
		lookupBinary("ping", "/bin/ping"),
		lookupBinary("dig", "/usr/bin/dig"),
		lookupBinary("host", "/usr/bin/host"),
		lookupBinary("nslookup", "/usr/bin/nslookup"),
		lookupBinary("lsof", "/usr/bin/lsof"),
		lookupBinary("killall", "/usr/bin/killall"),
		lookupBinary("fuser", "/usr/bin/fuser"),
		lookupBinary("mount", "/usr/bin/mount"),
		lookupBinary("umount", "/usr/bin/umount"),
		lookupBinary("lsblk", "/usr/bin/lsblk"),
		lookupBinary("blkid", "/usr/sbin/blkid"),
		lookupBinary("flock", "/usr/bin/flock"),
		lookupBinary("capsh", "/usr/sbin/capsh"),
		lookupBinary("getcap", "/usr/sbin/getcap"),
		lookupBinary("setcap", "/usr/sbin/setcap"),
		lookupBinary("iptables", "/usr/sbin/iptables"),
		lookupBinary("nft", "/usr/sbin/nft"),
		lookupBinary("nc", "/usr/bin/nc"),
		lookupBinary("rsync", "/usr/bin/rsync"),
		lookupBinary("ssh", "/usr/bin/ssh"),
	)
	return template
}

func openclawRootTemplate() Template {
	template := openclawAdminTemplate()
	template.Name = "openclaw-root"
	template.Description = "OpenClaw root rootfs level with package management, tracing, debugging, namespace, and filesystem tooling."
	template.Binaries = appendUniqueBinaries(template.Binaries,
		lookupBinary("sudo", "/usr/bin/sudo"),
		lookupBinary("apt", "/usr/bin/apt"),
		lookupBinary("apt-cache", "/usr/bin/apt-cache"),
		lookupBinary("apt-get", "/usr/bin/apt-get"),
		lookupBinary("gpg", "/usr/bin/gpg"),
		lookupBinary("strace", "/usr/bin/strace"),
		lookupBinary("gdb", "/usr/bin/gdb"),
		lookupBinary("nsenter", "/usr/bin/nsenter"),
		lookupBinary("socat", "/usr/bin/socat"),
		lookupBinary("parted", "/usr/sbin/parted"),
		lookupBinary("mkfs.ext4", "/usr/sbin/mkfs.ext4"),
		lookupBinary("e2fsck", "/usr/sbin/e2fsck"),
		lookupBinary("resize2fs", "/usr/sbin/resize2fs"),
		lookupBinary("tune2fs", "/usr/sbin/tune2fs"),
		lookupBinary("mkfs.xfs", "/usr/sbin/mkfs.xfs"),
		lookupBinary("xfs_repair", "/usr/sbin/xfs_repair"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		optionalRuntimeFile("/usr/share/keyrings/debian-archive-keyring.gpg", "/usr/share/keyrings/debian-archive-keyring.gpg"),
	)
	return template
}

func openclawSystemdTemplate() Template {
	template := openclawTemplate()
	template.Name = "openclaw-systemd"
	template.Description = "OpenClaw-oriented rootfs template with guest systemd tooling and systemd-ready directories."
	template.Directories = appendUniqueDirectories(template.Directories,
		directory("/dev", 0o755),
		directory("/sys", 0o755),
		directory("/sys/fs/cgroup", 0o755),
		directory("/etc/systemd/system", 0o755),
		directory("/usr/lib/systemd/system", 0o755),
		directory("/var/lib/systemd", 0o755),
		directory("/var/log/journal", 0o755),
	)
	template.Binaries = appendUniqueBinaries(template.Binaries,
		lookupBinary("systemd", "/usr/bin/systemd"),
		lookupBinary("systemctl", "/usr/bin/systemctl"),
		lookupBinary("journalctl", "/usr/bin/journalctl"),
		lookupBinary("systemd-tmpfiles", "/usr/bin/systemd-tmpfiles"),
	)
	template.RuntimeFiles = appendUniqueRuntimeFiles(template.RuntimeFiles,
		runtimeFile("/etc/passwd", "/etc/passwd"),
		runtimeFile("/etc/group", "/etc/group"),
		optionalRuntimeFile("/etc/os-release", "/etc/os-release"),
	)
	template.GeneratedFiles = appendUniqueGeneratedFiles(template.GeneratedFiles,
		generatedFile("/etc/machine-id", "", 0o644),
	)
	return template
}

var commonRuntimeDirectories = []Directory{
	directory("/proc", 0o755),
	directory("/tmp", 0o1777),
	directory("/run", 0o755),
}

var commonRuntimeFiles = []RuntimeFile{
	runtimeFile("/etc/hosts", "/etc/hosts"),
	runtimeFile("/etc/resolv.conf", "/etc/resolv.conf"),
	runtimeFile("/etc/nsswitch.conf", "/etc/nsswitch.conf"),
}

func cloneTemplate(template Template) Template {
	template.Directories = slices.Clone(template.Directories)
	template.Binaries = slices.Clone(template.Binaries)
	template.RuntimeTrees = slices.Clone(template.RuntimeTrees)
	template.RuntimeFiles = slices.Clone(template.RuntimeFiles)
	template.GeneratedFiles = slices.Clone(template.GeneratedFiles)
	return template
}

func directory(path string, mode uint32) Directory {
	return Directory{Path: path, Mode: mode}
}

func lookupBinary(name string, targetPath string) Binary {
	return Binary{
		TargetPath:       targetPath,
		LookupName:       name,
		CopyDependencies: true,
	}
}

func runtimeFile(hostPath string, targetPath string) RuntimeFile {
	return RuntimeFile{
		HostPath:   hostPath,
		TargetPath: targetPath,
	}
}

func runtimeTree(hostPath string, targetPath string) RuntimeTree {
	return RuntimeTree{
		HostPath:   hostPath,
		TargetPath: targetPath,
	}
}

func optionalRuntimeTree(hostPath string, targetPath string) RuntimeTree {
	return RuntimeTree{
		HostPath:   hostPath,
		TargetPath: targetPath,
		Optional:   true,
	}
}

func optionalRuntimeFile(hostPath string, targetPath string) RuntimeFile {
	return RuntimeFile{
		HostPath:   hostPath,
		TargetPath: targetPath,
		Optional:   true,
	}
}

func generatedFile(targetPath string, content string, mode uint32) GeneratedFile {
	return GeneratedFile{
		TargetPath: targetPath,
		Content:    content,
		Mode:       mode,
	}
}

func appendUniqueDirectories(existing []Directory, extra ...Directory) []Directory {
	for _, dir := range extra {
		if slices.ContainsFunc(existing, func(candidate Directory) bool {
			return candidate.Path == dir.Path
		}) {
			continue
		}
		existing = append(existing, dir)
	}
	return existing
}

func appendUniqueBinaries(existing []Binary, extra ...Binary) []Binary {
	for _, binary := range extra {
		if slices.ContainsFunc(existing, func(candidate Binary) bool {
			return candidate.TargetPath == binary.TargetPath
		}) {
			continue
		}
		existing = append(existing, binary)
	}
	return existing
}

func appendUniqueRuntimeTrees(existing []RuntimeTree, extra ...RuntimeTree) []RuntimeTree {
	for _, runtimeTree := range extra {
		if slices.ContainsFunc(existing, func(candidate RuntimeTree) bool {
			return candidate.TargetPath == runtimeTree.TargetPath
		}) {
			continue
		}
		existing = append(existing, runtimeTree)
	}
	return existing
}

func appendUniqueRuntimeFiles(existing []RuntimeFile, extra ...RuntimeFile) []RuntimeFile {
	for _, runtimeFile := range extra {
		if slices.ContainsFunc(existing, func(candidate RuntimeFile) bool {
			return candidate.TargetPath == runtimeFile.TargetPath && candidate.HostPath == runtimeFile.HostPath
		}) {
			continue
		}
		existing = append(existing, runtimeFile)
	}
	return existing
}

func appendUniqueGeneratedFiles(existing []GeneratedFile, extra ...GeneratedFile) []GeneratedFile {
	for _, generatedFile := range extra {
		if slices.ContainsFunc(existing, func(candidate GeneratedFile) bool {
			return candidate.TargetPath == generatedFile.TargetPath
		}) {
			continue
		}
		existing = append(existing, generatedFile)
	}
	return existing
}
