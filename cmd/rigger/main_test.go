package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, run([]string{"version"}, &out))
	assert.Equal(t, "rigger dev\n", out.String())
}

func TestRunValidate(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, run([]string{"validate", "../../example"}, &out))
	assert.Contains(t, out.String(), "ok")

	err := run([]string{"validate", t.TempDir()}, &out)
	require.ErrorContains(t, err, "read manifest")
}

func TestRunUnknown(t *testing.T) {
	require.ErrorContains(t, run([]string{"nope"}, &bytes.Buffer{}), `unknown command "nope"`)
	require.ErrorContains(t, run(nil, &bytes.Buffer{}), "usage")
}
