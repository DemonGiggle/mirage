package runner

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

var fallbackPublicResolvers = []string{
	"1.1.1.1",
	"8.8.8.8",
	"2606:4700:4700::1111",
	"2001:4860:4860::8888",
}

func prepareRoutedResolverOverride(rootfs string) error {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return fmt.Errorf("read host resolv.conf: %w", err)
	}

	override, changed, err := routedResolverConfig(data)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	tempFile, err := os.CreateTemp("", "mirage-routed-resolv-*.conf")
	if err != nil {
		return fmt.Errorf("create routed resolver override: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(override); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("write routed resolver override: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close routed resolver override: %w", err)
	}
	if err := applyBindMount(rootfs, bindMount{
		Source:   tempPath,
		Target:   "/etc/resolv.conf",
		ReadOnly: true,
	}); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("bind routed resolver override: %w", err)
	}
	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove routed resolver override staging file: %w", err)
	}
	return nil
}

func routedResolverConfig(data []byte) ([]byte, bool, error) {
	var preserved []string
	var publicResolvers []string
	changed := false

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			preserved = append(preserved, line)
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 2 || fields[0] != "nameserver" {
			preserved = append(preserved, line)
			continue
		}

		addr, err := netip.ParseAddr(fields[1])
		if err != nil {
			preserved = append(preserved, line)
			continue
		}
		if routedResolverAllowed(addr) {
			publicResolvers = appendIfMissing(publicResolvers, addr.String())
			continue
		}
		changed = true
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("scan host resolv.conf: %w", err)
	}

	if !changed {
		return nil, false, nil
	}
	if len(publicResolvers) == 0 {
		publicResolvers = append(publicResolvers, fallbackPublicResolvers...)
	}

	var b strings.Builder
	for _, line := range preserved {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, resolver := range publicResolvers {
		b.WriteString("nameserver ")
		b.WriteString(resolver)
		b.WriteByte('\n')
	}
	return []byte(b.String()), true, nil
}

func routedResolverAllowed(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	if addr.Is4() {
		if inPrefix(addr, "10.0.0.0/8") ||
			inPrefix(addr, "172.16.0.0/12") ||
			inPrefix(addr, "192.168.0.0/16") ||
			inPrefix(addr, "100.64.0.0/10") ||
			inPrefix(addr, "169.254.0.0/16") {
			return false
		}
		return true
	}
	if inPrefix(addr, "fc00::/7") || inPrefix(addr, "fe80::/10") {
		return false
	}
	return true
}

func inPrefix(addr netip.Addr, raw string) bool {
	prefix := netip.MustParsePrefix(raw)
	return prefix.Contains(addr)
}

func appendIfMissing(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
