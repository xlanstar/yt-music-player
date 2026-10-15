package ytmusic

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"yt-music-player/internal/innertube"
)

// What the fixtures were recorded from (see TestRecord).
const (
	searchQuery       = "daft punk"
	albumID           = "MPREb_7ltM34kr0mH"                  // Discovery
	playlistID        = "PLw_8I7j6_QFogcNFA-ZgwnDz7X8rvnVUN" // 151 tracks: one continuation
	missingPlaylistID = "PLdoesnotexist0000000000000000000"
	// chartPlaylistID is the first chart of charts.json, which the charts
	// fallback reads.
	chartPlaylistID = "OLAK5uy_k7TCLTpyYJf-tuuxK3NgI7SoEEw_2nvAI"
)

// exchange is one recorded request, without its context, and the file
// holding the response.
type exchange struct {
	Endpoint string         `json:"endpoint"`
	Body     map[string]any `json:"body"`
	File     string         `json:"file"`
}

const indexFile = "requests.json"

// fixtureClient serves the recorded responses for exactly the recorded
// requests, so that tests check what is asked as well as what is parsed.
// Anything else fails the test and gets a 404.
func fixtureClient(t *testing.T) *Client {
	t.Helper()
	return serverClient(t, fixtureHandler(t))
}

func fixtureHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", indexFile))
	if err != nil {
		t.Fatal(err)
	}
	var index []exchange
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint, body := readRequest(t, r)
		key := requestKey(endpoint, body)
		for _, ex := range index {
			if requestKey(ex.Endpoint, ex.Body) == key {
				http.ServeFile(w, r, filepath.Join("testdata", ex.File))
				return
			}
		}
		t.Errorf("unexpected request %s", key)
		http.NotFound(w, r)
	}
}

func serverClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(&innertube.Client{HTTP: srv.Client(), BaseURL: srv.URL + "/youtubei/v1/", HL: "en", Retries: -1}, nil)
}

// readRequest returns the endpoint and body of an InnerTube request,
// checking the parts every request shares.
func readRequest(t *testing.T, r *http.Request) (string, map[string]any) {
	t.Helper()
	endpoint, ok := strings.CutPrefix(r.URL.Path, "/youtubei/v1/")
	if r.Method != http.MethodPost || !ok || r.URL.Query().Get("prettyPrint") != "false" {
		t.Errorf("request = %s %s", r.Method, r.URL)
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Errorf("body is not JSON: %v", err)
	}
	if name := nav(body, "context", "client", "clientName"); name != "WEB_REMIX" {
		t.Errorf("context.client.clientName = %v", name)
	}
	delete(body, "context")
	return endpoint, body
}

func requestKey(endpoint string, body map[string]any) string {
	data, _ := json.Marshal(body, json.Deterministic(true)) // Sorted keys: a canonical form.
	return endpoint + " " + string(data)
}

// nav follows object keys and array indices through decoded JSON, for
// tests that read or edit raw requests and responses.
func nav(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}
