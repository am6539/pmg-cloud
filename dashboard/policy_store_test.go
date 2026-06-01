package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyStore_EmptyOnInit(t *testing.T) {
	ps, err := NewPolicyStore(t.TempDir())
	require.NoError(t, err)
	pol := ps.Get()
	assert.Empty(t, pol.Blocklist)
	assert.Empty(t, pol.Allowlist)
}

func TestPolicyStore_AddBlock(t *testing.T) {
	ps, err := NewPolicyStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, ps.AddRule("block", PolicyRule{Ecosystem: "npm", Name: "left-pad", Version: "*"}))
	pol := ps.Get()
	require.Len(t, pol.Blocklist, 1)
	assert.Equal(t, "left-pad", pol.Blocklist[0].Name)
}

func TestPolicyStore_AddAllow(t *testing.T) {
	ps, err := NewPolicyStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, ps.AddRule("allow", PolicyRule{Ecosystem: "npm", Name: "@acme/ui", Version: "*"}))
	require.Len(t, ps.Get().Allowlist, 1)
}

func TestPolicyStore_RejectsInvalidList(t *testing.T) {
	ps, err := NewPolicyStore(t.TempDir())
	require.NoError(t, err)
	err = ps.AddRule("nonsense", PolicyRule{Ecosystem: "npm", Name: "x", Version: "*"})
	require.Error(t, err)
}

func TestPolicyStore_RejectsEmptyName(t *testing.T) {
	ps, err := NewPolicyStore(t.TempDir())
	require.NoError(t, err)
	err = ps.AddRule("block", PolicyRule{Ecosystem: "npm", Name: "", Version: "*"})
	require.Error(t, err)
}

func TestPolicyStore_RemoveRule(t *testing.T) {
	ps, err := NewPolicyStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, ps.AddRule("block", PolicyRule{Ecosystem: "npm", Name: "left-pad", Version: "*"}))
	id := ps.Get().Blocklist[0].ID
	require.NoError(t, ps.RemoveRule("block", id))
	assert.Empty(t, ps.Get().Blocklist)
}

func TestPolicyStore_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	ps, err := NewPolicyStore(dir)
	require.NoError(t, err)
	require.NoError(t, ps.AddRule("block", PolicyRule{Ecosystem: "pypi", Name: "evil", Version: "1.0.0"}))

	ps2, err := NewPolicyStore(dir)
	require.NoError(t, err)
	require.Len(t, ps2.Get().Blocklist, 1)
	assert.Equal(t, "evil", ps2.Get().Blocklist[0].Name)
}
