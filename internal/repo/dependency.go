package repo

//go:generate go run go.uber.org/mock/mockgen -source=dependency.go -destination=mocks/dependency_mock.go -package=mocks

import (
	"context"

	"github.com/k0nsta/rigger/internal/sh"
)

// Runner runs external commands; a non-zero exit is reported in Result.Code, not as an error.
type Runner interface {
	Run(ctx context.Context, c sh.Cmd) (sh.Result, error)
}
