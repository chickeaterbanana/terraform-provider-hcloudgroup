package template

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

func TestRender_BasicVariables(t *testing.T) {
	src := "name={{.GroupName}} slot={{.SlotID}} gen={{.Generation}} server={{.ServerName}}"
	out, err := Render(src, slotctx.SlotContext{
		GroupName: "consul", SlotID: 1, Generation: 4, ServerName: "consul-1-4",
		Now: time.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, "name=consul slot=1 gen=4 server=consul-1-4", out)
}

func TestRender_PeersOrderedBySlotID(t *testing.T) {
	src := `{{range .Peers}}{{.SlotID}}={{.PrivateIP}};{{end}}`
	out, err := Render(src, slotctx.SlotContext{
		GroupName: "g", SlotID: 2, Generation: 1, ServerName: "g-2-1",
		Now: time.Now(),
		Peers: []slotctx.Peer{
			{SlotID: 0, PrivateIP: "10.0.0.10"},
			{SlotID: 1, PrivateIP: "10.0.0.11"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "0=10.0.0.10;1=10.0.0.11;", out)
}

func TestRender_EmptyTemplateReturnsEmpty(t *testing.T) {
	out, err := Render("", slotctx.SlotContext{Now: time.Now()})
	require.NoError(t, err)
	require.Equal(t, "", out)
}

func TestRender_BadTemplateReportsError(t *testing.T) {
	_, err := Render("{{.DoesNotClose", slotctx.SlotContext{Now: time.Now()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "user_data_template")
}

// Parse is the plan-time hook used by the resource's ValidateConfig.
// Empty input is a no-op so a missing user_data_template doesn't error.
func TestParse_EmptyOK(t *testing.T) {
	require.NoError(t, Parse(""))
}

// Parse must catch the same syntactic errors Render catches, but without
// executing the template (no slot data is available at plan time).
func TestParse_BadTemplateReportsError(t *testing.T) {
	err := Parse("{{ .DoesNotClose")
	require.Error(t, err)
	require.Contains(t, err.Error(), "user_data_template parse")
}

// Parse accepts templates that *would* fail at execute time (missing
// fields), because plan-time can't validate field references against the
// generated SlotContext. This is intentional — it just rejects syntax.
func TestParse_AcceptsExecuteTimeFailures(t *testing.T) {
	require.NoError(t, Parse(`{{ .NoSuchField }}`))
}

func TestRender_ConsulPeersExample(t *testing.T) {
	// Mirrors the spec section 9.2 example. Slot 0 has no peers; slot 1
	// sees slot 0 as a retry_join target.
	src := strings.TrimSpace(`
write_files:
  - path: /etc/consul.d/peers.json
    content: |
      {{ "{" }}"retry_join": [
        {{ range $i, $p := .Peers }}{{ if $i }},{{ end }}"{{ $p.PrivateIP }}"{{ end }}
      ]{{ "}" }}
`)
	slot0, err := Render(src, slotctx.SlotContext{
		GroupName: "consul", SlotID: 0, Generation: 1, ServerName: "consul-0-1",
		Now: time.Now(),
	})
	require.NoError(t, err)
	require.Contains(t, slot0, `"retry_join": [`)

	slot1, err := Render(src, slotctx.SlotContext{
		GroupName: "consul", SlotID: 1, Generation: 1, ServerName: "consul-1-1",
		Now:   time.Now(),
		Peers: []slotctx.Peer{{SlotID: 0, PrivateIP: "10.0.0.10"}},
	})
	require.NoError(t, err)
	require.Contains(t, slot1, `"10.0.0.10"`)
}
