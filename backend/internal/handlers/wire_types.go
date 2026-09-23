package handlers

// This file holds the wire shapes the handlers package serves in a response
// body, and nothing else.
// backend/tygo.yaml generates frontend/src/types/generated-handlers.ts from
// this file alone (include_files), because the rest of the package is full of
// request bodies, handler structs and interfaces that are not a JSON contract.
// tygo v0.2.21 filters by file, not by type (tygo/config.go, IsFileIgnored),
// so a served struct belongs here and anything else does not.

import "time"

// EnvEntry is the wire shape of one line of a stack's env file. It is
// bidirectional: it is both what GET /:id/env returns (EnvResponse.Entries)
// and what PUT /:id/env accepts (EnvRequest.Entries); the editor's draft row
// is derived from it (EnvEntryDraft in frontend/src/types/index.ts, extended
// by EnvEntryRow in frontend/src/components/stack/env-editor/types.ts). Its TypeScript counterpart is generated from this
// file into frontend/src/types/generated-handlers.ts (backend/tygo.yaml) and
// re-exported by frontend/src/types/index.ts; env_wire_contract_test.go pins
// the tag choices below on the real encoder.
//
// Sensitive carries no omitempty on purpose (agent-os-6wrb). TypeScript
// declares `sensitive: boolean` as REQUIRED, so omitting the key on false
// made the declared type a lie: every consumer read the absent value as
// false and was right only because undefined is falsy. It is a
// security-relevant masking hint, so the wire is matched to the strict
// declaration rather than the declaration loosened to a lossy wire.
//
// Comment keeps omitempty because TypeScript declares it OPTIONAL
// (`comment?: boolean`); that pair already agrees.
//
// Line carries no omitempty because every RESPONSE carries it and it is
// never 0 there: parseEnvFile increments lineNum before use, so it is 1-based
// at every construction site on the response path. The generated TypeScript therefore declares
// `line: number` as required, and that is the response contract. The REQUEST
// role is stated separately on the frontend, by the hand-written EnvEntryDraft
// in frontend/src/types/index.ts (the editor's draft row, which has no line
// until the file is saved and re-parsed); an absent line decodes here as 0
// and nothing on the request path reads it (agent-os-tuxp).
type EnvEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Line      int    `json:"line"`
	Sensitive bool   `json:"sensitive"`
	Comment   bool   `json:"comment,omitempty"`
}

// EnvResponse is the wire shape of GET /:id/env when the stack HAS an env file.
//
// HasEnvFile is always true here and is not omitempty: the no-file answer is a
// separate 200 payload carrying `{"hasEnvFile": false}` and nothing else
// (agent-os-bt5y), so the field has to be present on both branches for a client
// to discriminate on it.
//
// Raw is omitempty and Locked is set because the response is redacted for a
// session that has not re-entered its password: see redactEnvResponse. A caller
// must therefore treat a missing "raw" as "not authorised to see it", not as
// "the file is empty" — Locked tells the two apart.
type EnvResponse struct {
	HasEnvFile bool       `json:"hasEnvFile"`
	Filename   string     `json:"filename"`
	Entries    []EnvEntry `json:"entries"`
	Raw        string     `json:"raw,omitempty"`
	Locked     bool       `json:"locked,omitempty"`
}

// BuildCacheEntry is our own wire representation of a Docker build-cache
// record.
//
// The endpoint used to serialize build.CacheRecord from the Docker SDK
// straight to the wire, which made it the only PascalCase payload in the API
// and inherited an upstream tag typo: CacheRecord declares
// `json:" Parents,omitempty"` with a LEADING SPACE, so the field went out as
// " Parents" and the frontend's Parents was permanently undefined
// (agent-os-iuby). Declaring our own type closes both problems for good — a
// tag change upstream can no longer alter our contract.
//
// The deprecated CacheRecord.Parent (singular, deprecated in API v1.42) is
// deliberately not carried over; nothing consumed it.
type BuildCacheEntry struct {
	ID          string     `json:"id"`
	Parents     []string   `json:"parents,omitempty"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	InUse       bool       `json:"inUse"`
	Shared      bool       `json:"shared"`
	Size        int64      `json:"size"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt" tstype:"string | null,required"`
	UsageCount  int        `json:"usageCount"`
}
