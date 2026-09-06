package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realResticSnapshotsJSON is the VERBATIM stdout of `restic snapshots --json`,
// captured from restic 0.18.0 against a real repository holding one snapshot of
// exactly 200000 bytes. The pinned version is 0.19.1 (docker/Dockerfile:82).
//
// It is pasted literally rather than produced by json.Marshal(resticSnapshot{...}),
// because marshalling our own struct and unmarshalling it back is a tautology:
// it would pass even if the struct tag named a field restic does not emit. The
// only thing that can discriminate a wrong tag is restic's own bytes.
const realResticSnapshotsJSON = `[{"time":"2026-09-06T18:38:59.469213567+02:00","tree":"8ece9e2bf4059ab711b0f3fd033ede2cba7fc3ce22c7c6c709f4e39b427d4b49","paths":["/srv/stacks/mystack"],"hostname":"myhost","username":"edwin","uid":1000,"gid":1000,"tags":["stack:probe"],"program_version":"restic 0.18.0","summary":{"backup_start":"2026-09-06T18:38:59.469213567+02:00","backup_end":"2026-09-06T18:39:00.262108982+02:00","files_new":1,"files_changed":0,"files_unmodified":0,"dirs_new":7,"dirs_changed":0,"dirs_unmodified":0,"data_blobs":1,"tree_blobs":8,"data_added":203053,"data_added_packed":202500,"total_files_processed":1,"total_bytes_processed":200000},"id":"89fb47888feca4a33929e0783fe6f1c99edd9e7f087b8a79387d72d2ec248cb2","short_id":"89fb4788"}]`

// TestResticManager_ListSnapshots_PopulatesSizeBytes pins agent-os-kezb item 2.
//
// models.BackupSnapshot.SizeBytes is tagged `json:"sizeBytes,omitempty"` and the
// frontend renders it in a column headed "Size" (BackupsTab.tsx:472, :266), but
// nothing ever assigned it, so `omitempty` dropped the key on every response and
// that column could only ever show an em-dash. The value was already present in
// the JSON ListSnapshots parses -- summary.total_bytes_processed -- and
// resticSnapshot simply had no field for it, so no extra restic call is needed.
//
// total_bytes_processed, not data_added: a column headed "Size" beside a snapshot
// means the logical size of what the snapshot holds. data_added answers a
// different, unlabelled question (what this run cost the repository), and on an
// incremental snapshot the two differ by orders of magnitude.
func TestResticManager_ListSnapshots_PopulatesSizeBytes(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputData: []byte(realResticSnapshotsJSON)}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	result, err := m.ListSnapshots(context.Background(), "", 0)
	require.NoError(t, err)
	require.Len(t, result, 1)

	assert.Equal(t, int64(200000), result[0].SizeBytes,
		"SizeBytes must carry summary.total_bytes_processed from the snapshots JSON already being parsed; 0 means the key is dropped by omitempty and the frontend Size column renders an em-dash forever")

	// Two-sided control: the fields that were already parsed must survive, so a
	// regression that rewrote the mapping wholesale cannot pass by adding only
	// the field under test. 203053 (data_added) appearing here instead of
	// 200000 would mean the wrong summary field was wired up.
	assert.Equal(t, "89fb47888feca4a33929e0783fe6f1c99edd9e7f087b8a79387d72d2ec248cb2", result[0].ID)
	assert.Equal(t, "89fb4788", result[0].ShortID)
	assert.Equal(t, "myhost", result[0].Hostname)
	assert.Equal(t, []string{"stack:probe"}, result[0].Tags)
	assert.Equal(t, []string{"/srv/stacks/mystack"}, result[0].Paths)
	assert.NotEqual(t, int64(203053), result[0].SizeBytes,
		"203053 is data_added, the deduplicated delta this run added to the repo -- not the snapshot's size")
}

// TestResticManager_ListSnapshots_SizeBytesAbsentWhenNoSummary pins the
// degradation path. Snapshots written by restic < 0.17 carry no summary object
// at all. json.Unmarshal leaves the field zero, `omitempty` drops the key, and
// BackupsTab.tsx:266 renders the em-dash -- exactly today's behaviour, so no
// existing repository renders worse than it does now.
func TestResticManager_ListSnapshots_SizeBytesAbsentWhenNoSummary(t *testing.T) {
	t.Parallel()

	const preSummaryJSON = `[{"time":"2026-05-30T10:00:00Z","paths":["/srv/stacks/old"],"hostname":"oldhost","tags":["stack:old"],"id":"aaaabbbbccccdddd","short_id":"aaaabbbb"}]`

	runner := &fakeRunner{outputData: []byte(preSummaryJSON)}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	result, err := m.ListSnapshots(context.Background(), "", 0)
	require.NoError(t, err)
	require.Len(t, result, 1)

	assert.Equal(t, int64(0), result[0].SizeBytes,
		"a snapshot with no summary must leave SizeBytes zero so omitempty drops the key")
	assert.Equal(t, "aaaabbbb", result[0].ShortID, "the rest of the snapshot must still parse")
}
