package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestImageDelete_UntaggedIsNeverNull pins agent-os-413n at the wire.
//
// classifyImageDeleteResponse declared untagged as a nil slice and sent it as
// details.untagged, so a Docker response with only Deleted entries rendered
// `"untagged": null` where the TypeScript DeleteResult declares string[]. The
// body is rendered through renderResult, the writer deleteImage uses, so the
// assertion is on the bytes a client receives. The last row is the control: a
// response that did untag something must still carry it.
func TestImageDelete_UntaggedIsNeverNull(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		resp []image.DeleteResponse
		want []string
	}{
		{"deleted only", []image.DeleteResponse{{Deleted: "sha256:abc"}}, []string{}},
		{"entry with neither field", []image.DeleteResponse{{}}, []string{}},
		{"control: untagged and deleted", []image.DeleteResponse{{Untagged: "app:1"}, {Deleted: "sha256:abc"}}, []string{"app:1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			renderResult(c, classifyImageDeleteResponse(tc.resp))

			var body struct {
				Details map[string]json.RawMessage `json:"details"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body=%s", w.Body.String())
			raw, ok := body.Details["untagged"]
			require.True(t, ok, "details.untagged missing, body=%s", w.Body.String())
			require.NotEqual(t, "null", string(raw), "body=%s", w.Body.String())
			var got []string
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, tc.want, got, "body=%s", w.Body.String())
		})
	}
}
