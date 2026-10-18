package ytmusic

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"0:59", 59},
		{"3:05", 185},
		{"10:01", 601},
		{"1:02:03", 3723},
		{" 4:20 ", 260},
		{"", 0},
		{"59", 0},
		{"1:2:3:4", 0},
		{"a:bc", 0},
		{"2,343", 0},
		{"-1:00", 0},
		{"+1:00", 0},
		{"1::00", 0},
	} {
		if got := seconds(tc.in); got != tc.want {
			t.Errorf("seconds(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// FuzzParse feeds the parsers any JSON, starting from the fixtures, to
// show that a changed response never panics them.
func FuzzParse(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.json"))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	for _, s := range []string{
		`null`, `[]`, `{"contents":5}`, `{"contents":{"twoColumnBrowseResultsRenderer":[]}}`,
		`{"musicResponsiveListItemRenderer":{"flexColumns":[{},null,{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":7}]}}}]}}`,
		`{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"x"},"fixedColumns":"3:00"}}`,
		`{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"t"}]},"navigationEndpoint":{"watchPlaylistEndpoint":{"playlistId":"RDCLAKx"}}}}`,
		`{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"t"}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"VL"}}}}`,
		`{"contents":{"singleColumnBrowseResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"gridRenderer":{"items":[{"musicNavigationButtonRenderer":{"buttonText":{"runs":[{"text":"m"}]},"clickCommand":{"browseEndpoint":{"params":"p"}},"solid":{"leftStripeColor":-1e300}}}]}}]}}}}]}}}`,
		`{"continuationContents":{"sectionListContinuation":{"contents":[{"musicCarouselShelfRenderer":{"contents":[null,7]}}],"continuations":[{"nextContinuationData":{"continuation":5}}]}}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		var row item
		if json.Unmarshal(data, &row, lenient) == nil {
			parseSong(row)
			parseAlbumRow(row)
			parseItem(row)
			ranked([]item{row})
		}
		resp, err := decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		albumPage("MPREb_x", resp)
		_, rows, _ := playlistPage("PLx", resp)
		playlistSongs(nil, rows)
		continuation(rows)
		alert(resp)
		cont := resp.ContinuationContents.SectionList
		parseShelves(sectionList(resp).sections())
		parseShelves(cont.sections())
		sectionList(resp).next()
		cont.next()
		explorePage(resp)
		chartPage(resp)
		moodsPage(resp)
		accountMenu(resp)
		signedOut(resp)
		parseRows(searchRows(resp), parseSong)
		libraryContents(resp).rows()
	})
}

// TestPlaylistStops: a playlist whose pages never end, or repeat, costs a
// bounded number of requests.
func TestPlaylistStops(t *testing.T) {
	row := func(i int) string {
		return fmt.Sprintf(`{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"v%d"},`+
			`"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":`+
			`{"text":{"runs":[{"text":"Song %d"}]}}}]}}`, i, i)
	}
	page := func(start int, token string) string {
		rows := make([]string, 0, 101)
		for i := range 100 {
			rows = append(rows, row(start+i))
		}
		rows = append(rows, `{"continuationItemRenderer":`+
			`{"continuationEndpoint":{"continuationCommand":{"token":"`+token+`"}}}}`)
		return strings.Join(rows, ",")
	}
	for _, tc := range []struct {
		name      string
		repeat    bool
		wantSongs int
		wantCalls int32
	}{
		{"endless", false, maxPlaylistSongs, maxPlaylistSongs / 100},
		{"repeating", true, 200, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, body := readRequest(t, r)
				request := int(calls.Add(1))
				token := strconv.Itoa(request)
				if tc.repeat {
					token = "same"
				}
				if body["browseId"] != nil {
					_, _ = fmt.Fprintf(w, `{"contents":{"twoColumnBrowseResultsRenderer":{"secondaryContents":`+
						`{"sectionListRenderer":{"contents":[{"musicPlaylistShelfRenderer":{"contents":[%s]}}]}}}}}`,
						page(0, token))
					return
				}
				_, _ = fmt.Fprintf(w, `{"onResponseReceivedActions":[{"appendContinuationItemsAction":`+
					`{"continuationItems":[%s]}}]}`, page((request-1)*100, token))
			})
			pl, songs, err := c.Playlist(t.Context(), "PLendless")
			if err != nil {
				t.Fatal(err)
			}
			if len(songs) != tc.wantSongs || len(pl.Songs) != tc.wantSongs || calls.Load() != tc.wantCalls {
				t.Errorf("got %d songs in %d calls, want %d in %d",
					len(songs), calls.Load(), tc.wantSongs, tc.wantCalls)
			}
		})
	}
}

func TestDecodeSkipsChangedFields(t *testing.T) {
	data := readBytes(t, "playlist.json")
	changed := bytes.ReplaceAll(data, []byte(`"width":400`), []byte(`"width":"400"`))
	changed = bytes.Replace(changed, []byte(`"text":"`), []byte(`"text":7,"_":"`), 1)
	if bytes.Equal(data, changed) {
		t.Fatal("nothing changed")
	}
	resp, err := decode(bytes.NewReader(changed))
	if err != nil {
		t.Fatal(err)
	}
	if _, rows, _ := playlistPage(playlistID, resp); len(playlistSongs(nil, rows)) != 100 {
		t.Errorf("got %d songs, want 100", len(playlistSongs(nil, rows)))
	}
}
