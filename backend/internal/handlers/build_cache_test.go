package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The defect this file exists to prevent, stated against the upstream type
// itself so it stays true (and keeps failing loudly) if Docker ever fixes the
// tag: build.CacheRecord declares `json:" Parents,omitempty"` with a leading
// space, so serializing it directly emits a field name no client can address.
func TestBuildCacheRecord_UpstreamParentsTagIsUnaddressable(t *testing.T) {
	raw, err := json.Marshal(build.CacheRecord{ID: "abc", Parents: []string{"p1"}})
	require.NoError(t, err)

	assert.Contains(t, string(raw), `" Parents"`,
		"upstream still emits the space-prefixed field; if this fails Docker fixed the tag and the comment above can go")

	// What a client that spells the field correctly actually receives.
	var asClientSeesIt struct {
		Parents []string `json:"Parents"`
	}
	require.NoError(t, json.Unmarshal(raw, &asClientSeesIt))
	assert.Nil(t, asClientSeesIt.Parents,
		"a correctly-spelled Parents cannot read the space-prefixed field — this is why the frontend saw undefined")
}

// Pins the exact bytes GET /resources/build-cache puts on the wire. The
// frontend asserts against this same literal payload in
// frontend/src/lib/__tests__/build-cache-contract.test.ts.
func TestToBuildCacheEntries_WireShape(t *testing.T) {
	created := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	lastUsed := time.Date(2026, 8, 8, 11, 30, 0, 0, time.UTC)

	entries := toBuildCacheEntries([]*build.CacheRecord{
		{
			ID:          "cache-1",
			Parent:      "deprecated-parent",
			Parents:     []string{"p1", "p2"},
			Type:        "regular",
			Description: "RUN go build",
			InUse:       true,
			Shared:      false,
			Size:        4096,
			CreatedAt:   created,
			LastUsedAt:  &lastUsed,
			UsageCount:  3,
		},
	})
	require.Len(t, entries, 1)

	raw, err := json.Marshal(entries[0])
	require.NoError(t, err)
	got := string(raw)

	assert.JSONEq(t, `{
		"id": "cache-1",
		"parents": ["p1", "p2"],
		"type": "regular",
		"description": "RUN go build",
		"inUse": true,
		"shared": false,
		"size": 4096,
		"createdAt": "2026-08-08T10:00:00Z",
		"lastUsedAt": "2026-08-08T11:30:00Z",
		"usageCount": 3
	}`, got)

	// The two things that broke: a space-prefixed key, and the deprecated
	// singular Parent leaking through.
	assert.NotContains(t, got, `" `, "no field name may start with a space")
	assert.NotContains(t, got, "deprecated-parent")
}

func TestToBuildCacheEntries_OmitsParentsWhenEmptyAndNullsLastUsed(t *testing.T) {
	entries := toBuildCacheEntries([]*build.CacheRecord{{ID: "cache-2", Type: "regular"}})
	require.Len(t, entries, 1)

	raw, err := json.Marshal(entries[0])
	require.NoError(t, err)

	assert.NotContains(t, string(raw), "parents")
	assert.Contains(t, string(raw), `"lastUsedAt":null`,
		"lastUsedAt must be present as null, not omitted — the frontend types it as string | null")
}

func TestToBuildCacheEntries_EmptyAndNil(t *testing.T) {
	// A non-nil empty slice, so the endpoint emits [] rather than null.
	assert.NotNil(t, toBuildCacheEntries(nil))
	assert.Len(t, toBuildCacheEntries(nil), 0)

	assert.Len(t, toBuildCacheEntries([]*build.CacheRecord{nil, {ID: "ok"}}), 1)
}
