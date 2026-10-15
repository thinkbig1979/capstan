package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-8d8a: the prune confirm dialogs read which stacks a volume or
// network prune would take from these routes.
var pruneImpactPaths = []string{"/api/resources/volumes/prune-impact", "/api/resources/networks/prune-impact"}

func TestPruneImpactRoutesServeTheImpact(t *testing.T) {
	impact := &services.PruneImpact{
		Stacks:          []services.PruneImpactStack{{Stack: "shop", Items: []string{"pgdata", "proxy"}}},
		UncheckedStacks: []string{"broken"},
	}
	for _, path := range pruneImpactPaths {
		t.Run(path, func(t *testing.T) {
			w, _ := hostSpaceGet(hostSpaceRouter(t, &hostSpaceFake{impact: impact}), path)
			require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
			assert.JSONEq(t, `{"stacks":[{"stack":"shop","items":["pgdata","proxy"]}],"uncheckedStacks":["broken"]}`, w.Body.String())
		})
	}
}

// A failed read is an error response, never an empty impact: "no stack
// affected" would let the prune go through without the typed confirmation.
func TestPruneImpactRouteErrorsAreNotAnEmptyImpact(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"docker unavailable", fmt.Errorf("listing networks: %w", services.ErrDockerUnavailable), http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE"},
		{"other failure", errors.New("listing stacks: disk I/O error"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		for _, path := range pruneImpactPaths {
			t.Run(tc.name+" "+path, func(t *testing.T) {
				w, body := hostSpaceGet(hostSpaceRouter(t, &hostSpaceFake{err: tc.err}), path)
				assert.Equal(t, tc.status, w.Code, "body = %s", w.Body.String())
				assert.Equal(t, tc.code, body["code"])
				assert.NotContains(t, body, "stacks")
			})
		}
	}
}

func TestPruneImpactRoutesRefuseWithoutTheService(t *testing.T) {
	r := hostSpaceRouter(t, nil)
	for _, path := range pruneImpactPaths {
		t.Run(path, func(t *testing.T) {
			w, body := hostSpaceGet(r, path)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body = %s", w.Body.String())
			assert.Equal(t, "DOCKER_UNAVAILABLE", body["code"])
		})
	}
}
