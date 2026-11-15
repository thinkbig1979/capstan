package services

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-b6u8 item 1: two functions derive a stack's status from its
// containers. composeStackStatus (docker_lifecycle.go) is what Status reports
// after a lifecycle action; BuildStackStatuses (docker.go) is what the stack
// list shows. A stack that reads one status on its page after an action and
// another in the list is the defect this table exists to catch, so it feeds the
// same container sets to both and requires the same answer.
//
// The one listed exception: every container paused. BuildStackStatuses says
// "paused" and composeStackStatus says "partial", on purpose
// (BackupService.observeRunState reads "partial" as running; BuildStackStatuses'
// docblock, agent-os-n97z). A project with no service containers is not here
// either: BuildStackStatuses leaves it out of the map and the caller decides.
func TestStackStatusRulesAgree_B6u8(t *testing.T) {
	sets := [][]string{
		{"running"},
		{"running", "running", "running"},
		{"exited"},
		{"exited", "dead"},
		{"created"},
		{"created", "exited"},
		{"running", "exited"},
		{"running", "created"},
		{"running", "dead"},
		{"restarting"},
		{"running", "restarting"},
		{"exited", "restarting"},
		{"running", "paused"},
		{"exited", "paused"},
		{"removing"},
		{"running", "removing"},
	}
	exceptions := map[string]string{
		"paused":        "paused/partial",
		"paused,paused": "paused/partial",
	}
	sets = append(sets, []string{"paused"}, []string{"paused", "paused"})

	for _, states := range sets {
		key := strings.Join(states, ",")
		t.Run(key, func(t *testing.T) {
			compose := make([]models.Container, 0, len(states))
			snapshot := make([]models.DashboardContainerInfo, 0, len(states))
			for i, st := range states {
				compose = append(compose, models.Container{ID: fmt.Sprint(i), State: st})
				snapshot = append(snapshot, models.DashboardContainerInfo{ID: fmt.Sprint(i), ProjectName: "p", State: st})
			}
			fromCompose := composeStackStatus(compose)
			fromList := BuildStackStatuses(snapshot)["p"].Status

			if want, ok := exceptions[key]; ok {
				if got := fromList + "/" + fromCompose; got != want {
					t.Fatalf("listed exception changed: list/compose = %s, want %s", got, want)
				}
				return
			}
			if fromCompose != fromList {
				t.Fatalf("containers %v: composeStackStatus = %q, BuildStackStatuses = %q; the stack page and the list disagree", states, fromCompose, fromList)
			}
		})
	}
}
