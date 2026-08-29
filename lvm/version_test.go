package lvm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetVersionParsesReport(t *testing.T) {
	fake := &fakeRunner{output: []byte(
		"  LVM version:     2.03.38(2) (2025-12-15)\n" +
			"  Library version: 1.02.212 (2025-12-15)\n" +
			"  Driver version:  4.50.0\n" +
			"  Configuration:   ./configure --prefix=/usr\n")}
	client := New(WithRunner(fake))

	version, err := client.GetVersion(t.Context(), VersionOptions{})

	require.NoError(t, err)
	assert.Equal(t, []string{"version"}, fake.calls[0].Args())
	assert.Equal(t, Version{
		LVM:     "2.03.38(2) (2025-12-15)",
		Library: "1.02.212 (2025-12-15)",
		Driver:  "4.50.0",
	}, version)
}

func TestGetVersionWithoutDriverLine(t *testing.T) {
	fake := &fakeRunner{output: []byte("  LVM version:     2.03.16(2) (2022-05-18)\n")}
	client := New(WithRunner(fake))

	version, err := client.GetVersion(t.Context(), VersionOptions{})

	require.NoError(t, err)
	assert.Equal(t, "2.03.16(2) (2022-05-18)", version.LVM)
	assert.Empty(t, version.Driver)
}
