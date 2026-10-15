package ytmusic

import (
	"net/http"
	"strings"
	"testing"
)

// Errors reach logs and toasts, so they may not quote the response.
func TestDecodeErrorKeepsResponseOut(t *testing.T) {
	for _, body := range []string{
		`{"responseContext":{"visitorData":"CgtSECRET%3D%3D"}, "x": tru}`,
		`{"visitorData":"CgtSECRET%3D%3D`,
		"{\"visitorData\":\"CgtSECRET\xff%3D%3D\"}",
		"{\"visitorData\":\"CgtSECRET\x01%3D%3D\"}",
		`{"visitorData":"CgtSECRET%3D%3D","visitorData":"CgtSECRET%3D%3D"}`,
		`["CgtSECRET%3D%3D"]`,
	} {
		c := serverClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		_, err := c.call(t.Context(), "browse", map[string]any{"browseId": "FEmusic_home"})
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%q: error %v", body, err)
		}
	}
}
