package runner

import (
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestWriteRoutedResolverOverrideFileMakesWorldReadableFile(t *testing.T) {
	path, err := writeRoutedResolverOverrideFile([]byte(`nameserver 1.1.1.1
`))
	if err != nil {
		t.Fatalf("writeRoutedResolverOverrideFile returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat override file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("expected override file mode 0644, got %#o", got)
	}
}

func TestRoutedResolverOverrideConfigFallsBackWhenHostConfigMissing(t *testing.T) {
	override, changed, err := routedResolverOverrideConfig(nil, true)
	if err != nil {
		t.Fatalf("routedResolverOverrideConfig returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected missing host resolv.conf to trigger a fallback override")
	}

	got := string(override)
	for _, resolver := range []string{"nameserver 1.1.1.1", "nameserver 8.8.8.8"} {
		if !strings.Contains(got, resolver) {
			t.Fatalf("expected fallback resolver %q in %q", resolver, got)
		}
	}
}

func TestRoutedResolverConfigKeepsPublicResolvers(t *testing.T) {
	input := []byte("nameserver 1.1.1.1\noptions edns0\n")

	output, changed, err := routedResolverConfig(input)
	if err != nil {
		t.Fatalf("routedResolverConfig returned error: %v", err)
	}
	if changed {
		t.Fatalf("expected public resolver config to stay unchanged, got %q", string(output))
	}
}

func TestRoutedResolverConfigReplacesPrivateResolversWithFallbacks(t *testing.T) {
	input := []byte("nameserver 192.168.1.1\nnameserver 127.0.0.53\nsearch corp.example\n")

	output, changed, err := routedResolverConfig(input)
	if err != nil {
		t.Fatalf("routedResolverConfig returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected private resolvers to trigger an override")
	}

	got := string(output)
	if !strings.Contains(got, "search corp.example\n") {
		t.Fatalf("expected search domain to be preserved, got %q", got)
	}
	for _, resolver := range []string{"nameserver 1.1.1.1", "nameserver 8.8.8.8"} {
		if !strings.Contains(got, resolver) {
			t.Fatalf("expected fallback resolver %q in %q", resolver, got)
		}
	}
	for _, blocked := range []string{"192.168.1.1", "127.0.0.53"} {
		if strings.Contains(got, blocked) {
			t.Fatalf("expected blocked resolver %q to be removed from %q", blocked, got)
		}
	}
}

func TestRoutedResolverConfigPreservesReachablePublicResolverWhenMixed(t *testing.T) {
	input := []byte("nameserver 192.168.1.1\nnameserver 9.9.9.9\n")

	output, changed, err := routedResolverConfig(input)
	if err != nil {
		t.Fatalf("routedResolverConfig returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected mixed resolver config to trigger an override")
	}

	got := string(output)
	if !strings.Contains(got, "nameserver 9.9.9.9") {
		t.Fatalf("expected public resolver to be preserved, got %q", got)
	}
	if strings.Contains(got, "nameserver 1.1.1.1") {
		t.Fatalf("did not expect fallback resolvers when a public resolver already exists, got %q", got)
	}
}

func TestRoutedResolverAllowedRejectsPrivateAndStubRanges(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.53":           false,
		"10.0.0.2":             false,
		"172.16.1.2":           false,
		"192.168.1.1":          false,
		"100.64.0.1":           false,
		"169.254.1.10":         false,
		"fe80::1":              false,
		"fc00::1":              false,
		"1.1.1.1":              true,
		"8.8.8.8":              true,
		"2606:4700:4700::1111": true,
		"2001:4860:4860::8888": true,
	}

	for raw, want := range cases {
		addr := mustParseAddr(t, raw)
		if got := routedResolverAllowed(addr); got != want {
			t.Fatalf("routedResolverAllowed(%q) = %v, want %v", raw, got, want)
		}
	}
}

func mustParseAddr(t *testing.T, raw string) (addr netip.Addr) {
	t.Helper()
	var err error
	addr, err = netip.ParseAddr(raw)
	if err != nil {
		t.Fatalf("ParseAddr(%q) returned error: %v", raw, err)
	}
	return addr
}
