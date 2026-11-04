package main

import (
	"os"
	"testing"

	"github.com/k0nsta/rigger/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunWithOwnGitConfig(m)) }
