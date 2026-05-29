package spec

import (
	"strings"
	"testing"
)

func TestLoadNetworkPolicyYAMLCanonicalOfflinePolicy(t *testing.T) {
	policy := mustLoadNetworkPolicy(t, `networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
`)

	if policy.Version != 1 {
		t.Fatalf("unexpected version: %d", policy.Version)
	}
	if policy.Loopback.Default != PolicyAllow {
		t.Fatalf("unexpected loopback default: %q", policy.Loopback.Default)
	}
	if policy.Ingress.Default != PolicyDeny || len(policy.Ingress.Rules) != 0 {
		t.Fatalf("unexpected ingress policy: %#v", policy.Ingress)
	}
	if policy.Egress.Default != PolicyDeny || len(policy.Egress.Rules) != 0 {
		t.Fatalf("unexpected egress policy: %#v", policy.Egress)
	}
}

func TestLoadNetworkPolicyYAMLNormalizesSelectors(t *testing.T) {
	policy := mustLoadNetworkPolicy(t, `networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules:
      - name: allow-admin
        action: allow
        source:
          ip: 2001:0db8:0000:0000:0000:0000:0000:0001
        protocol: tcp
        ports: [22]
  egress:
    default: deny
    rules:
      - name: allow-lan
        action: allow
        destination:
          cidr: 192.168.1.10/16
        protocol: any
      - name: allow-github
        action: allow
        destination:
          domain: GitHub.COM.
        protocol: tcp
        ports: [443]
`)

	if got := policy.Ingress.Rules[0].Source.IP; got != "2001:db8::1" {
		t.Fatalf("expected normalized IP, got %q", got)
	}
	if got := policy.Egress.Rules[0].Destination.CIDR; got != "192.168.0.0/16" {
		t.Fatalf("expected normalized CIDR, got %q", got)
	}
	if got := policy.Egress.Rules[1].Destination.Domain; got != "github.com" {
		t.Fatalf("expected normalized domain, got %q", got)
	}
}

func TestLoadNetworkPolicyYAMLRejectsMissingTopLevelPolicy(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`{}`))
	requirePolicyError(t, err, "networkPolicy is required")
}

func TestLoadNetworkPolicyYAMLRejectsUnknownFields(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
  surprise: true
`))
	requirePolicyError(t, err, "surprise")
}

func TestLoadNetworkPolicyYAMLRejectsInvalidVersion(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 2
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
`))
	requirePolicyError(t, err, "networkPolicy.version must be 1")
}

func TestLoadNetworkPolicyYAMLRejectsMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing version",
			body: `networkPolicy:
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
`,
			want: "networkPolicy.version is required",
		},
		{
			name: "missing loopback",
			body: `networkPolicy:
  version: 1
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
`,
			want: "networkPolicy.loopback is required",
		},
		{
			name: "missing ingress rules",
			body: `networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
  egress:
    default: deny
    rules: []
`,
			want: "ingress.rules is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadNetworkPolicyYAML([]byte(tc.body))
			requirePolicyError(t, err, tc.want)
		})
	}
}

func TestLoadNetworkPolicyYAMLRejectsInvalidDefaults(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: maybe
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules: []
`))
	requirePolicyError(t, err, "networkPolicy.loopback.default must be allow or deny")
}

func TestLoadNetworkPolicyYAMLRejectsMixedSelector(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules:
      - action: allow
        source:
          ip: 203.0.113.10
          cidr: 203.0.113.0/24
        protocol: tcp
        ports: [22]
  egress:
    default: deny
    rules: []
`))
	requirePolicyError(t, err, "must define exactly one of ip, cidr, or domain")
}

func TestLoadNetworkPolicyYAMLRejectsDomainInIngress(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules:
      - action: allow
        source:
          domain: admin.example.com
        protocol: tcp
        ports: [22]
  egress:
    default: deny
    rules: []
`))
	requirePolicyError(t, err, "domain is only valid for egress destinations")
}

func TestLoadNetworkPolicyYAMLRejectsDirectionalMisuse(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules:
      - action: allow
        destination:
          ip: 203.0.113.10
        protocol: tcp
        ports: [22]
  egress:
    default: deny
    rules: []
`))
	requirePolicyError(t, err, "destination")
}

func TestLoadNetworkPolicyYAMLRejectsLoopbackSelectors(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules:
      - action: allow
        destination:
          cidr: 127.0.0.0/8
        protocol: any
`))
	requirePolicyError(t, err, "cidr must not be loopback")
}

func TestLoadNetworkPolicyYAMLRejectsPortsWithProtocolAny(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules:
      - action: allow
        destination:
          cidr: 203.0.113.0/24
        protocol: any
        ports: [443]
`))
	requirePolicyError(t, err, "ports are only valid with tcp or udp")
}

func TestLoadNetworkPolicyYAMLRejectsPortRangeSyntax(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules:
      - action: allow
        destination:
          ip: 203.0.113.10
        protocol: tcp
        ports: ["8000-8010"]
`))
	requirePolicyError(t, err, "cannot unmarshal")
}

func TestLoadNetworkPolicyYAMLRejectsDuplicateRuleNamesAndPorts(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules:
      - name: api
        action: allow
        destination:
          ip: 203.0.113.10
        protocol: tcp
        ports: [443, 443]
      - name: api
        action: deny
        destination:
          ip: 203.0.113.11
        protocol: tcp
        ports: [443]
`))
	requirePolicyError(t, err, `name "api" is duplicated`)
	requirePolicyError(t, err, "duplicate port 443")
}

func TestLoadNetworkPolicyYAMLRejectsInvalidDomain(t *testing.T) {
	_, err := LoadNetworkPolicyYAML([]byte(`networkPolicy:
  version: 1
  loopback:
    default: allow
  ingress:
    default: deny
    rules: []
  egress:
    default: deny
    rules:
      - action: allow
        destination:
          domain: https://api.openai.com/v1
        protocol: tcp
        ports: [443]
`))
	requirePolicyError(t, err, "domain must not include")
}

func mustLoadNetworkPolicy(t *testing.T, yamlText string) NetworkPolicy {
	t.Helper()
	policy, err := LoadNetworkPolicyYAML([]byte(yamlText))
	if err != nil {
		t.Fatalf("LoadNetworkPolicyYAML returned error: %v", err)
	}
	return policy
}

func requirePolicyError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}
