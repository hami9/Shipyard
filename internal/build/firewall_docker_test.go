//go:build docker

package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The firewall script runs as root in a new user and network namespace
// (`unshare -rn`), so it changes no real firewall. Docker is a stub that
// reports the backend under test.
func runFirewall(t *testing.T, backend, steps string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("../../deploy/firewall/shipyard-firewall.sh")
	if err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho "+backend+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("unshare", "-rn", "bash", "-c", "set -e; fw() { bash "+script+" \"$@\"; }; "+steps)
	cmd.Env = append(os.Environ(), "SHIPYARD_DOCKER="+stub)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// P5.7a (ADR-0009): apply sends traffic from every builder bridge to the host
// into a DROP chain, and drops link-local destinations (cloud metadata) in
// DOCKER-USER; applying twice adds nothing; remove leaves nothing.
func TestFirewallScript(t *testing.T) {
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("needs unshare")
	}
	src, err := os.ReadFile("../../deploy/firewall/shipyard-firewall.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "readonly IFACE='"+BridgePrefix+"+'") {
		t.Fatalf("the script does not match bridges named %s*", BridgePrefix)
	}

	out, err := runFirewall(t, "iptables", `
		iptables -w -N DOCKER-USER; ip6tables -w -N DOCKER-USER
		fw apply; fw apply
		echo '=== v4'; iptables -w -S; echo '=== v6'; ip6tables -w -S
		fw remove
		echo '=== after'; iptables -w -S; ip6tables -w -S`)
	if err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	v4, rest, _ := strings.Cut(strings.SplitN(out, "=== v4\n", 2)[1], "=== v6\n")
	v6, after, _ := strings.Cut(rest, "=== after\n")
	for _, want := range []string{
		"-A INPUT -i sybuild-+ -j SHIPYARD-BUILD-IN",
		"-A DOCKER-USER -i sybuild-+ -j SHIPYARD-BUILD-FWD",
		"-A SHIPYARD-BUILD-IN -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN",
		"-A SHIPYARD-BUILD-IN -j DROP",
		"-A SHIPYARD-BUILD-FWD -d 169.254.0.0/16 -j DROP",
		"-A SHIPYARD-BUILD-FWD -j RETURN",
	} {
		if n := strings.Count(v4, want+"\n"); n != 1 {
			t.Errorf("IPv4 has %d of %q, want 1:\n%s", n, want, v4)
		}
	}
	if !strings.Contains(v6, "-A SHIPYARD-BUILD-FWD -d fd00:ec2::254/128 -j DROP\n") || strings.Count(v6, "-j SHIPYARD-BUILD-IN\n") != 1 {
		t.Errorf("IPv6 rules:\n%s", v6)
	}
	if strings.Contains(after, "SHIPYARD") || !regexp.MustCompile(`(?m)^-N DOCKER-USER$`).MatchString(after) {
		t.Errorf("after remove:\n%s", after)
	}

	// The nftables backend has no DOCKER-USER chain: refuse, change nothing.
	out, err = runFirewall(t, "nftables", `iptables -w -N DOCKER-USER; fw apply || { echo "exit $?"; iptables -w -S; }`)
	if err != nil || !strings.Contains(out, "nftables firewall backend") || !strings.Contains(out, "exit 1") || strings.Contains(out, "SHIPYARD") {
		t.Errorf("nftables backend: %v\n%s", err, out)
	}
	// Without Docker's chain (Docker not started), apply refuses.
	out, err = runFirewall(t, "iptables", `fw apply || echo "exit $?"`)
	if err != nil || !strings.Contains(out, "start Docker first") || !strings.Contains(out, "exit 1") {
		t.Errorf("no DOCKER-USER: %v\n%s", err, out)
	}
}
