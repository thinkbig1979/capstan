package services

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-zrm2: updateComposeContainer's pull/up errors carried compose
// output unredacted into the update result. Fixture: redact_compose_sdbr_test.go.

func TestUpdateComposeContainer_RedactsErrorOutput(t *testing.T) {
	for _, verb := range []string{"pull", "up"} {
		t.Run(verb, func(t *testing.T) {
			svc, stack := fvk3Service(t, "")
			stubComposeContextScript(t, sdbrStepScript(verb, "1"))

			err := svc.updateComposeContainer(context.Background(), stack, "web", true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "compose "+verb+" failed")
			assertFvk3Redacted(t, verb+" error", err.Error())
		})
	}
}

// A long pull prints progress first and its real error last, so the error must
// keep compose's full text: a 500-byte cap would keep the secret's line and
// drop the diagnosis.
func TestUpdateComposeContainer_LongOutputKeepsDiagnosis(t *testing.T) {
	pad := strings.Repeat("x", 600)
	svc, stack := fvk3Service(t, "")
	stubComposeContextScript(t, `case " $* " in *" pull "*) `+
		`printf '%s\n' 'password=`+fvk3StackSecret+`' '`+pad+`'; printf '%s\n' '`+fvk3Diagnosis+`' >&2; exit 1 ;; esac; exit 0`)

	err := svc.updateComposeContainer(context.Background(), stack, "web", true)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), fvk3StackSecret)
	assert.Contains(t, err.Error(), fvk3Diagnosis, "the diagnosis after 500 bytes was cut")
}
