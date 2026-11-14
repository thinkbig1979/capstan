//go:build race

package handlers

// timingFactor stretches a wall-clock bound in a test to what the race
// detector costs: the 40000-row chain in
// TestEnvAfterQuote_zpbi_LongTailParsesInLinearTime takes about 0.4 s
// without it and 3.4 s with it (agent-os-olcn, PR #736's race job).
const timingFactor = 6
