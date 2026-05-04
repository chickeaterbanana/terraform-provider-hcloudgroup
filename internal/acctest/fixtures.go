package acctest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/crypto/ssh"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// Resource-name prefix every acctest uses. The sweeper filters by this
// prefix, so anything not matching it stays untouched.
const TestPrefix = "tfacc"

// FixtureLabel is the identity tag the suite stamps onto every shared
// fixture (network, ssh key, jump host) at creation time. The sweeper
// gates fixture deletion on this label, so a same-named resource in a
// production project is never touched.
const (
	fixtureLabelKey   = "tfacc.io/fixture"
	fixtureLabelValue = "shared"
)

// Shared fixtures: created lazily on first use, torn down in TestMain.
var (
	fixOnce sync.Once
	fixErr  error
	shared  *Suite
)

// Suite is the shared per-run fixture set: a Network, an SSH key, and a
// jump host. Reused across every acctest in one `go test` run.
type Suite struct {
	NetworkID        int64
	NetworkName      string
	SubnetCIDR       string
	SSHKeyName       string
	SSHKeyID         int64
	PublicKeyOpenSSH string
	PrivateKeyPEM    string
	PrivateKeyPath   string
	JumpServerID     int64
	JumpPublicIP     string

	hc *hcloud.Client
}

// Get returns the shared suite, creating it on first call. Callers must
// have passed PreCheck before calling Get.
//
// The suite lives for the lifetime of the test binary; teardown is the
// responsibility of TestMain in whichever package runs the tests. See
// the AccTestMain helper.
func Get(t *testing.T) *Suite {
	t.Helper()
	fixOnce.Do(func() {
		shared, fixErr = bootstrap(t)
	})
	if fixErr != nil {
		t.Fatalf("acctest fixture bootstrap failed: %v", fixErr)
	}
	return shared
}

// AccTestMain is the standard TestMain body for any test package that
// drives acceptance tests. It runs the requested tests, then tears down
// the shared suite (jump host, network, ssh key) and runs the leftover
// sweeper. Use it like:
//
//	func TestMain(m *testing.M) { acctest.AccTestMain(m) }
//
// If TF_ACC is unset (hermetic mode), no teardown runs.
func AccTestMain(m *testing.M) {
	code := m.Run()
	if os.Getenv(EnvAcceptance) != "" && os.Getenv(EnvHcloudToken) != "" {
		Teardown()
		hc := hcloud.NewClient(hcloud.WithToken(os.Getenv(EnvHcloudToken)))
		_ = SweepLeftoverResources(context.Background(), hc)
	}
	os.Exit(code)
}

// Teardown destroys the suite. Called from TestMain after all tests
// complete. Idempotent.
func Teardown() {
	if shared == nil {
		return
	}
	shared.destroy(context.Background())
	shared = nil
}

func bootstrap(t *testing.T) (*Suite, error) {
	t.Helper()
	hc := hcloud.NewClient(hcloud.WithToken(os.Getenv(EnvHcloudToken)))
	s := &Suite{hc: hc}
	ctx := context.Background()

	// Pre-sweep: kill anything left over from a prior failed run.
	if err := SweepLeftoverResources(ctx, hc); err != nil {
		t.Logf("pre-sweep warning: %v", err)
	}

	if err := s.ensureNetwork(ctx); err != nil {
		return nil, fmt.Errorf("ensureNetwork: %w", err)
	}
	if err := s.ensureSSHKey(ctx); err != nil {
		return nil, fmt.Errorf("ensureSSHKey: %w", err)
	}
	if err := s.ensureJumpHost(ctx); err != nil {
		return nil, fmt.Errorf("ensureJumpHost: %w", err)
	}
	return s, nil
}

func (s *Suite) ensureNetwork(ctx context.Context) error {
	s.NetworkName = TestPrefix + "-shared-net"
	s.SubnetCIDR = "10.99.0.0/16"

	existing, _, err := s.hc.Network.GetByName(ctx, s.NetworkName)
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.Labels[fixtureLabelKey] != fixtureLabelValue {
			return fmt.Errorf("network %q exists but is missing the %q=%q identity label "+
				"— refusing to adopt it (probably not a sandbox project)",
				s.NetworkName, fixtureLabelKey, fixtureLabelValue)
		}
		s.NetworkID = existing.ID
		return nil
	}
	_, ipNet, _ := net.ParseCIDR(s.SubnetCIDR)
	created, _, err := s.hc.Network.Create(ctx, hcloud.NetworkCreateOpts{
		Name:    s.NetworkName,
		IPRange: ipNet,
		Subnets: []hcloud.NetworkSubnet{{
			Type:        hcloud.NetworkSubnetTypeCloud,
			IPRange:     ipNet,
			NetworkZone: hcloud.NetworkZoneEUCentral,
		}},
		Labels: map[string]string{fixtureLabelKey: fixtureLabelValue},
	})
	if err != nil {
		return err
	}
	s.NetworkID = created.ID
	return nil
}

func (s *Suite) ensureSSHKey(ctx context.Context) error {
	s.SSHKeyName = TestPrefix + "-shared-key"

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)
	s.PublicKeyOpenSSH = strings.TrimSpace(string(pubBytes))

	// Encode the private key as OpenSSH so `ssh -i` accepts it.
	pkBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return err
	}
	pemBytes := pem.EncodeToMemory(pkBlock)
	s.PrivateKeyPEM = string(pemBytes)

	tmp, err := os.CreateTemp("", "tfacc-priv-*.key")
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(pemBytes); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	s.PrivateKeyPath = tmp.Name()

	// If a stale shared key exists in hcloud (e.g., previous run), delete
	// it before re-uploading. The fingerprints differ across runs. Refuse
	// to delete one that's missing our fixture label — we may have
	// stumbled on someone else's key with a coincidentally matching name.
	if existing, _, err := s.hc.SSHKey.GetByName(ctx, s.SSHKeyName); err == nil && existing != nil {
		if existing.Labels[fixtureLabelKey] != fixtureLabelValue {
			return fmt.Errorf("ssh key %q exists but is missing the %q=%q identity label "+
				"— refusing to delete it (probably not a sandbox project)",
				s.SSHKeyName, fixtureLabelKey, fixtureLabelValue)
		}
		if _, err := s.hc.SSHKey.Delete(ctx, existing); err != nil {
			return fmt.Errorf("delete stale ssh_key: %w", err)
		}
	}
	created, _, err := s.hc.SSHKey.Create(ctx, hcloud.SSHKeyCreateOpts{
		Name:      s.SSHKeyName,
		PublicKey: strings.TrimSpace(string(pubBytes)),
		Labels:    map[string]string{fixtureLabelKey: fixtureLabelValue},
	})
	if err != nil {
		return err
	}
	s.SSHKeyID = created.ID
	return nil
}

func (s *Suite) ensureJumpHost(ctx context.Context) error {
	jumpName := TestPrefix + "-jump"

	existing, _, err := s.hc.Server.GetByName(ctx, jumpName)
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.Labels[fixtureLabelKey] != fixtureLabelValue {
			return fmt.Errorf("server %q exists but is missing the %q=%q identity label "+
				"— refusing to adopt it (probably not a sandbox project)",
				jumpName, fixtureLabelKey, fixtureLabelValue)
		}
		s.JumpServerID = existing.ID
		s.JumpPublicIP = existing.PublicNet.IPv4.IP.String()
		return nil
	}

	res, _, err := s.hc.Server.Create(ctx, hcloud.ServerCreateOpts{
		Name:       jumpName,
		ServerType: &hcloud.ServerType{Name: "cx23"},
		Image:      &hcloud.Image{Name: "debian-13"},
		Location:   &hcloud.Location{Name: "fsn1"},
		Networks:   []*hcloud.Network{{ID: s.NetworkID}},
		SSHKeys:    []*hcloud.SSHKey{{ID: s.SSHKeyID}},
		Labels: map[string]string{
			fixtureLabelKey:         fixtureLabelValue,
			TestPrefix + ".io/role": "jump",
		},
	})
	if err != nil {
		return err
	}
	s.JumpServerID = res.Server.ID

	// Wait for create + reachability.
	if err := s.hc.Action.WaitForFunc(ctx, nil, res.Action); err != nil {
		return fmt.Errorf("wait for jump create: %w", err)
	}
	for _, a := range res.NextActions {
		_ = s.hc.Action.WaitForFunc(ctx, nil, a)
	}

	// Re-fetch to capture the public IPv4 once it's allocated. Hetzner
	// reports the create action complete before the public IPv4 is
	// always populated on the resource, so poll briefly.
	var srv *hcloud.Server
	ipDeadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(ipDeadline) {
		srv, _, err = s.hc.Server.GetByID(ctx, res.Server.ID)
		if err != nil {
			return err
		}
		if srv != nil && srv.PublicNet.IPv4.IP != nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if srv == nil || srv.PublicNet.IPv4.IP == nil {
		return fmt.Errorf("jump host has no public IPv4 after 2m wait")
	}
	s.JumpPublicIP = srv.PublicNet.IPv4.IP.String()

	// Wait until SSH is actually accepting connections. Cloud-init
	// usually takes ~30-60s on cx22.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", s.JumpPublicIP+":22", 3*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("jump host SSH never came up at %s", s.JumpPublicIP)
}

// JumpSSHCommand returns the shell snippet that runs cmd on a managed VM
// at privateIP, hopping through the jump host. The result is suitable for
// embedding into HCL action `command` strings.
//
// Note: the action runner uses /bin/sh -c, so quoting matters. We rely on
// the caller writing simple commands (no shell metacharacters that would
// need escaping).
func (s *Suite) JumpSSHCommand(privateIP, cmd string) string {
	keyPath := s.PrivateKeyPath
	jump := s.JumpPublicIP
	return fmt.Sprintf(
		`ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null `+
			`-o ConnectTimeout=5 -i %s `+
			`-o ProxyCommand="ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i %s -W %%h:%%p root@%s" `+
			`root@%s -- %s`,
		keyPath, keyPath, jump, privateIP, cmd,
	)
}

// destroy tears the suite down. Errors are logged to stderr (not
// returned) so subsequent cleanup steps still run, but the network
// delete deliberately waits for the server delete action so it doesn't
// race against an attached jump host.
func (s *Suite) destroy(ctx context.Context) {
	if s.JumpServerID != 0 {
		res, _, err := s.hc.Server.DeleteWithResult(ctx, &hcloud.Server{ID: s.JumpServerID})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[acctest] jump host delete: %v\n", err)
		}
		if res != nil && res.Action != nil {
			if err := s.hc.Action.WaitForFunc(ctx, nil, res.Action); err != nil {
				fmt.Fprintf(os.Stderr, "[acctest] wait for jump delete: %v\n", err)
			}
		}
	}
	if s.SSHKeyID != 0 {
		if _, err := s.hc.SSHKey.Delete(ctx, &hcloud.SSHKey{ID: s.SSHKeyID}); err != nil {
			fmt.Fprintf(os.Stderr, "[acctest] ssh key delete: %v\n", err)
		}
	}
	if s.NetworkID != 0 {
		if _, err := s.hc.Network.Delete(ctx, &hcloud.Network{ID: s.NetworkID}); err != nil {
			fmt.Fprintf(os.Stderr, "[acctest] network delete: %v (likely a stranded server still attached)\n", err)
		}
	}
	if s.PrivateKeyPath != "" {
		_ = os.Remove(s.PrivateKeyPath)
	}
}

// SweepLeftoverResources removes test-suite resources from the
// configured Hetzner project. Two scopes:
//
//  1. Servers managed by the provider whose group label starts with
//     TestPrefix. Identified by the provider's standard managed-by label
//     plus the group-name prefix.
//  2. Shared fixtures (jump host, network, ssh key) whose name matches
//     the test naming convention AND whose labels include the
//     fixtureLabel identity tag.
//
// The label gate on shared fixtures is the critical safety guard: if a
// production project happens to contain a resource named "tfacc-jump"
// (for whatever reason), it is NOT deleted unless it also carries the
// fixture identity label. The provider sweep is similarly gated by the
// hcloudgroup-provider managed-by label.
//
// Server deletes are awaited before the network delete so the
// associated network is not held attached.
func SweepLeftoverResources(ctx context.Context, hc *hcloud.Client) error {
	// Provider-managed servers with our test-group prefix.
	selector := fmt.Sprintf("%s=%s", hcloudx.LabelManagedBy, hcloudx.ManagedByValue)
	servers, err := hc.Server.AllWithOpts(ctx, hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: selector, PerPage: 50},
	})
	if err != nil {
		return err
	}
	var pendingActions []*hcloud.Action
	for _, s := range servers {
		group := s.Labels[hcloudx.LabelGroup]
		if !strings.HasPrefix(group, TestPrefix) {
			continue
		}
		res, _, err := hc.Server.DeleteWithResult(ctx, &hcloud.Server{ID: s.ID})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[acctest sweep] delete server %d: %v\n", s.ID, err)
			continue
		}
		if res != nil && res.Action != nil {
			pendingActions = append(pendingActions, res.Action)
		}
	}

	// Stale jump host — only if it carries our fixture label.
	if jump, _, err := hc.Server.GetByName(ctx, TestPrefix+"-jump"); err == nil && jump != nil {
		if jump.Labels[fixtureLabelKey] == fixtureLabelValue {
			res, _, err := hc.Server.DeleteWithResult(ctx, &hcloud.Server{ID: jump.ID})
			if err != nil {
				fmt.Fprintf(os.Stderr, "[acctest sweep] delete jump host: %v\n", err)
			} else if res != nil && res.Action != nil {
				pendingActions = append(pendingActions, res.Action)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[acctest sweep] skipping server %q: missing fixture label (not ours)\n", jump.Name)
		}
	}

	// Wait for server deletes before touching the network — Hetzner refuses
	// to delete a Network while servers are still attached, even if those
	// servers' delete actions are in flight.
	for _, act := range pendingActions {
		if err := hc.Action.WaitForFunc(ctx, nil, act); err != nil {
			fmt.Fprintf(os.Stderr, "[acctest sweep] wait for server delete: %v\n", err)
		}
	}

	// Stale shared ssh key — only if labeled.
	if k, _, err := hc.SSHKey.GetByName(ctx, TestPrefix+"-shared-key"); err == nil && k != nil {
		if k.Labels[fixtureLabelKey] == fixtureLabelValue {
			if _, err := hc.SSHKey.Delete(ctx, k); err != nil {
				fmt.Fprintf(os.Stderr, "[acctest sweep] delete ssh key: %v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[acctest sweep] skipping ssh key %q: missing fixture label (not ours)\n", k.Name)
		}
	}

	// Stale shared network — only if labeled.
	if n, _, err := hc.Network.GetByName(ctx, TestPrefix+"-shared-net"); err == nil && n != nil {
		if n.Labels[fixtureLabelKey] == fixtureLabelValue {
			if _, err := hc.Network.Delete(ctx, n); err != nil {
				fmt.Fprintf(os.Stderr, "[acctest sweep] delete network: %v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[acctest sweep] skipping network %q: missing fixture label (not ours)\n", n.Name)
		}
	}
	return nil
}

// MustHcloud returns a hcloud client driven by the same token as the
// provider. Used by acceptance tests to make out-of-band assertions
// (label checks, orphan injection, out-of-band deletion).
func MustHcloud(t *testing.T) *hcloud.Client {
	t.Helper()
	tok := os.Getenv(EnvHcloudToken)
	if tok == "" {
		t.Fatal(EnvHcloudToken + " unset")
	}
	return hcloud.NewClient(hcloud.WithToken(tok))
}

// RandName builds a per-test resource name `tfacc-<prefix>-<unique>` that
// is guaranteed to fit the schema's 52-char group-name budget.
func RandName(t *testing.T, prefix string) string {
	t.Helper()
	// Use the test name itself as the unique suffix — stable within a
	// run, distinct across tests. Lowercase + dashes only.
	clean := strings.ToLower(t.Name())
	clean = strings.NewReplacer("/", "-", "_", "-").Replace(clean)
	out := fmt.Sprintf("%s-%s-%s", TestPrefix, prefix, clean)
	if len(out) > 52 {
		out = out[:52]
	}
	out = strings.TrimRight(out, "-")
	return out
}

var _ = filepath.Join // keep filepath import for any future helpers without churn
