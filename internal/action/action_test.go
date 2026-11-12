package action

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpandVars(t *testing.T) {
	home := t.TempDir()
	env := &Env{Home: home, Vars: map[string]string{"CODE": filepath.Join(home, "code")}}

	assert.Equal(t, filepath.Join(home, "code", "x"), env.Expand("${CODE}/x"))
	assert.Equal(t, filepath.Join(home, "code", "x"), env.Expand("~/code/x"))
	assert.Equal(t, home, env.Expand("~"))
	assert.Equal(t, "${UNKNOWN}/x", env.Expand("${UNKNOWN}/x"))
	assert.Equal(t, "$CODE/x", env.Expand("$CODE/x"))
}
