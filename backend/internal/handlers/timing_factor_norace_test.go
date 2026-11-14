//go:build !race

package handlers

// timingFactor stretches a wall-clock bound in a test to what the race
// detector costs; without it there is nothing to stretch.
const timingFactor = 1
