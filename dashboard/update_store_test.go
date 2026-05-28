package dashboard

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateStore_EmptyOnInit(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	cfg := us.GetConfig()
	assert.Empty(t, cfg.TargetVersion)
	assert.Empty(t, cfg.Binaries)
}

func TestUpdateStore_SetTargetVersion(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, us.SetTargetVersion("v1.2.0"))
	assert.Equal(t, "v1.2.0", us.GetConfig().TargetVersion)
}

func TestUpdateStore_StoreBinaryMeta(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	meta := BinaryMeta{SHA256: "abc123", Size: 1000}
	require.NoError(t, us.StoreBinaryMeta("linux", "amd64", meta))
	got, ok := us.GetConfig().Binaries["linux/amd64"]
	require.True(t, ok)
	assert.Equal(t, "abc123", got.SHA256)
	assert.Equal(t, int64(1000), got.Size)
}

func TestUpdateStore_BinaryPath(t *testing.T) {
	dir := t.TempDir()
	us, err := NewUpdateStore(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "binaries", "pmg-linux-amd64"), us.BinaryPath("linux", "amd64"))
	assert.Equal(t, filepath.Join(dir, "binaries", "pmg-windows-amd64.exe"), us.BinaryPath("windows", "amd64"))
}

func TestUpdateStore_UpdateInfoForAgent_NoTarget(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	info := us.UpdateInfoForAgent("linux", "amd64", "dev")
	assert.False(t, info.UpdateAvailable)
}

func TestUpdateStore_UpdateInfoForAgent_SameVersion(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, us.SetTargetVersion("v1.2.0"))
	require.NoError(t, us.StoreBinaryMeta("linux", "amd64", BinaryMeta{SHA256: "abc", Size: 100}))
	info := us.UpdateInfoForAgent("linux", "amd64", "v1.2.0")
	assert.False(t, info.UpdateAvailable)
}

func TestUpdateStore_UpdateInfoForAgent_OlderVersion(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, us.SetTargetVersion("v1.2.0"))
	require.NoError(t, us.StoreBinaryMeta("linux", "amd64", BinaryMeta{SHA256: "abc123", Size: 100}))
	info := us.UpdateInfoForAgent("linux", "amd64", "v1.1.0")
	require.True(t, info.UpdateAvailable)
	assert.Equal(t, "v1.2.0", info.Version)
	assert.Equal(t, "/download/pmg-linux-amd64", info.DownloadURL)
	assert.Equal(t, "abc123", info.SHA256)
}

func TestUpdateStore_UpdateInfoForAgent_NoBinaryForPlatform(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, us.SetTargetVersion("v1.2.0"))
	info := us.UpdateInfoForAgent("darwin", "arm64", "v1.0.0")
	assert.False(t, info.UpdateAvailable)
}

func TestUpdateStore_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	us, err := NewUpdateStore(dir)
	require.NoError(t, err)
	require.NoError(t, us.SetTargetVersion("v1.2.0"))
	require.NoError(t, us.StoreBinaryMeta("linux", "amd64", BinaryMeta{SHA256: "abc", Size: 100}))
	us2, err := NewUpdateStore(dir)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.0", us2.GetConfig().TargetVersion)
	_, ok := us2.GetConfig().Binaries["linux/amd64"]
	assert.True(t, ok)
}

func TestUpdateStore_DeleteBinaryMeta(t *testing.T) {
	us, err := NewUpdateStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, us.StoreBinaryMeta("linux", "amd64", BinaryMeta{SHA256: "abc", Size: 100}))
	require.NoError(t, us.DeleteBinaryMeta("linux", "amd64"))
	_, ok := us.GetConfig().Binaries["linux/amd64"]
	assert.False(t, ok)
}
