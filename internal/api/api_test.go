package api

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/notice"
)

// roundTrip sends f through a fresh gob stream and decodes it into a zero
// Frame, as a receiver does.
func roundTrip(t *testing.T, f *Frame) Frame {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(f); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got Frame
	if err := gob.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func testSong(i int) catalog.Song {
	n := strconv.Itoa(i)
	return catalog.Song{
		ID: "song" + n, Title: "Title " + n, Artist: "Artist " + n,
		Album: "Album " + n, AlbumID: "MPREb_" + n, Seconds: 180 + i, Art: catalog.Art("art" + n),
	}
}

func testAlbum() catalog.Album {
	return catalog.Album{ID: "MPREb_a", Title: "Album", Artist: "Artist", Year: 2024, Kind: catalog.KindEP, Art: "art-a"}
}

func testPlaylist() catalog.Playlist {
	return catalog.Playlist{ID: "PL1", Name: "Mix", Owner: "Owner", Art: "art-p", Songs: []string{"song1", "song2"}}
}

func testArtist() catalog.Artist {
	return catalog.Artist{Name: "Artist", Art: "art-r", ID: "UC1"}
}

func testAccount() catalog.Account {
	return catalog.Account{Name: "Lan", Handle: "@lan", Photo: "photo"}
}

func testLibrary() *catalog.Library {
	return &catalog.Library{
		Playlists: []catalog.Playlist{testPlaylist()},
		Albums:    []catalog.Album{testAlbum()},
		Artists:   []catalog.Artist{testArtist()},
		Songs:     []catalog.Song{testSong(1)},
	}
}

func testState() *State {
	return &State{
		Playing: true, Waiting: true, Position: 81.5, Duration: 213, Volume: 0.7, Muted: true,
		Repeat: RepeatOne, Shuffled: true, Current: "song3", Index: 3,
	}
}

// fullFrame sets every field of every wire type (TestWireTypes checks), in
// one Frame though a peer sets one part at a time.
func fullFrame() *Frame {
	songs := []catalog.Song{testSong(1), testSong(2)}
	return &Frame{
		ID: 7,
		Hello: &Hello{
			Proto: Proto, Version: "1.2.3", Role: RoleTUI, Subscribe: true, PID: 4242,
		},
		Call: &Call{
			Method: MethodPlay,
			IDs:    &IDsArgs{IDs: []string{"a", "b"}, Start: 1, Next: true, Songs: songs},
			Index:  &IndexArgs{Index: 2},
			Move:   &MoveArgs{From: 1, To: 4},
			Seek:   &SeekArgs{Seconds: 12.5},
			Volume: &VolumeArgs{Volume: 0.4},
			Query:  &QueryArgs{Query: "lofi"},
			Ref:    &RefArgs{ID: "MPREb_a"},
			Like:   &LikeArgs{ID: "song1", Liked: true, Song: new(testSong(1))},
			SignIn: &SignInArgs{Credentials: "SID=x"},
			Part:   &PartArgs{Part: PartLiked},
			Hand:   &HandoffArgs{Wait: 5 * time.Second},
		},
		Reply: &Reply{
			Err:      &Error{Code: CodeInvalidCredentials, Detail: "why"},
			Results:  &catalog.Results{Songs: songs, Albums: []catalog.Album{testAlbum()}},
			Album:    &AlbumReply{Album: testAlbum(), Songs: songs},
			Playlist: &PlaylistReply{Playlist: testPlaylist(), Songs: songs},
			Home: &catalog.Home{Shelves: []catalog.Shelf{{
				Title: "Listen again", Strapline: "Lan",
				Items: []catalog.Item{{
					Kind: catalog.ItemArtist, Song: testSong(1), Album: testAlbum(),
					Playlist: testPlaylist(), Artist: testArtist(),
				}},
			}}},
			Explore: &catalog.Explore{
				NewReleases: []catalog.Album{testAlbum()},
				Moods:       []catalog.Mood{{Name: "Chill", Tint: catalog.Tint{L: 0.5, C: 0.1, H: 200}, ID: "params"}},
				Trending:    songs,
			},
			Account: new(testAccount()),
			Library: testLibrary(),
			Status: &Status{
				Version: "1.2.3", Proto: Proto, State: *testState(), Song: testSong(3),
				Length: 42, SignedIn: true, Account: testAccount(),
			},
		},
		Cancel: true,
		Event: &Event{
			State: testState(),
			Queue: &Queue{IDs: []string{"song1", "song2"}},
			Songs: songs,
			Likes: &Likes{
				Full: true, IDs: []string{"song1"}, Songs: songs,
				Added: []string{"song2"}, Removed: []string{"song3"},
			},
			Account: &AccountEvent{Account: testAccount(), SignedIn: true, Switched: true},
			Library: testLibrary(),
			Failed:  &FailedEvent{Part: PartLibrary, Err: &Error{Code: CodeInternal, Detail: "x"}},
			Notice: &notice.Notice{
				Kind: notice.KindFailure, Title: notice.PlayFailed, Detail: notice.Reason,
				Args: []string{"Title", "why"}, N: 3, Topic: notice.TopicPlayback,
			},
			Handoff: &HandoffEvent{By: 4242, Wait: 5 * time.Second},
		},
	}
}

// TestWireTypes holds that every wire type crosses gob whole: no func,
// chan or interface, no unexported field gob would drop.
func TestWireTypes(t *testing.T) {
	checkWireType(t, reflect.TypeFor[Frame](), "Frame", map[reflect.Type]bool{})

	f := fullFrame()
	checkAllSet(t, reflect.ValueOf(f).Elem(), "Frame")
	if got := roundTrip(t, f); !reflect.DeepEqual(&got, f) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, *f)
	}
}

func checkWireType(t *testing.T, typ reflect.Type, path string, seen map[reflect.Type]bool) {
	t.Helper()
	if seen[typ] {
		return
	}
	seen[typ] = true
	switch typ.Kind() {
	case reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer, reflect.Uintptr,
		reflect.Complex64, reflect.Complex128, reflect.Map:
		t.Errorf("%s is a %s", path, typ.Kind())
	case reflect.Pointer, reflect.Slice, reflect.Array:
		checkWireType(t, typ.Elem(), path, seen)
	case reflect.Struct:
		for field := range typ.Fields() {
			if !field.IsExported() {
				t.Errorf("%s.%s is unexported", path, field.Name)
				continue
			}
			checkWireType(t, field.Type, path+"."+field.Name, seen)
		}
	default:
	}
}

// checkAllSet fails for each zero field under v, so that a field added to
// a wire type joins fullFrame.
func checkAllSet(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	if v.IsZero() {
		t.Errorf("fullFrame leaves %s zero", path)
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		checkAllSet(t, v.Elem(), path)
	case reflect.Slice:
		for i := range v.Len() {
			checkAllSet(t, v.Index(i), fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Struct:
		for i := range v.NumField() {
			checkAllSet(t, v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	default:
	}
}

// Gob sends a pointer to a zero struct, so a seek to 0 still carries its
// args and validates.
func TestZeroArgsArrive(t *testing.T) {
	got := roundTrip(t, &Frame{ID: 1, Call: &Call{Method: MethodSeek, Seek: &SeekArgs{}}})
	if got.Call == nil || got.Call.Seek == nil {
		t.Fatalf("got %+v, want SeekArgs", got.Call)
	}
	if err := got.Call.Validate(); err != nil {
		t.Error(err)
	}
}

// A newer peer's Frame, with fields this Proto lacks, still decodes.
func TestForwardCompatible(t *testing.T) {
	type newerState struct {
		Playing  bool
		Position float64
		Lyrics   string // new
	}
	type newerEvent struct {
		State   *newerState
		Karaoke *struct{ Line int } // new
	}
	type newerFrame struct {
		ID       uint32
		Event    *newerEvent
		Priority int // new
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	sent := newerFrame{
		ID: 0, Priority: 9,
		Event: &newerEvent{State: &newerState{Playing: true, Position: 3, Lyrics: "la"}, Karaoke: &struct{ Line int }{2}},
	}
	for range 2 {
		if err := enc.Encode(&sent); err != nil {
			t.Fatal(err)
		}
	}
	dec := gob.NewDecoder(&buf)
	for range 2 {
		var got Frame
		if err := dec.Decode(&got); err != nil {
			t.Fatal(err)
		}
		want := Frame{Event: &Event{State: &State{Playing: true, Position: 3}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

func TestEnumsRejectZero(t *testing.T) {
	for _, tc := range []struct {
		name         string
		zero, beyond bool
		first        bool
	}{
		{"Method", Method(0).Valid(), numMethods.Valid(), MethodPing.Valid()},
		{"Part", Part(0).Valid(), numParts.Valid(), PartProfile.Valid()},
		{"Repeat", Repeat(0).Valid(), numRepeats.Valid(), RepeatOff.Valid()},
		{"ErrorCode", ErrorCode(0).Valid(), numCodes.Valid(), CodeNotFound.Valid()},
	} {
		if tc.zero || tc.beyond || !tc.first {
			t.Errorf("%s: 0 valid %v, past the last valid %v, 1 valid %v", tc.name, tc.zero, tc.beyond, tc.first)
		}
	}
	if (&Hello{Role: RoleCLI}).Validate() == nil {
		t.Error("a Hello without a Proto validates")
	}
	if (&Hello{Proto: Proto}).Validate() == nil {
		t.Error("a Hello without a Role validates")
	}
	// A newer peer's Proto and Role still greet.
	if err := (&Hello{Proto: Proto + 1, Role: RoleDaemon + 1}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestMethodNames(t *testing.T) {
	names := map[string]bool{}
	for m := MethodPing; m < numMethods; m++ {
		name := m.String()
		if name == "" || strings.HasPrefix(name, "Method(") || names[name] {
			t.Errorf("method %d is named %q", m, name)
		}
		names[name] = true
	}
	if got := Method(0).String(); got != "Method(0)" {
		t.Errorf("Method(0) = %q", got)
	}
}

func TestCallValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		call Call
		ok   bool
	}{
		{"no args", Call{Method: MethodToggle}, true},
		{"its args", Call{Method: MethodEnqueue, IDs: &IDsArgs{IDs: []string{"a"}}}, true},
		{"handoff", Call{Method: MethodHandoff, Hand: &HandoffArgs{Wait: time.Second}}, true},
		{"refresh", Call{Method: MethodRefresh, Part: &PartArgs{Part: PartProfile}}, true},
		{"zero method", Call{}, false},
		{"unknown method", Call{Method: numMethods}, false},
		{"missing args", Call{Method: MethodSeek}, false},
		{"unwanted args", Call{Method: MethodPing, Seek: &SeekArgs{}}, false},
		{"wrong args", Call{Method: MethodSearch, Ref: &RefArgs{ID: "x"}}, false},
		{"two args", Call{Method: MethodSearch, Query: &QueryArgs{}, Ref: &RefArgs{}}, false},
		{"zero part", Call{Method: MethodRefresh, Part: &PartArgs{}}, false},
	} {
		err := tc.call.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
}

func TestSignInArgsHideCredentials(t *testing.T) {
	args := &SignInArgs{Credentials: "SID=topsecret"}
	for _, s := range []string{fmt.Sprint(args), fmt.Sprintf("%v %+v %#v", *args, *args, *args)} {
		if strings.Contains(s, "topsecret") {
			t.Errorf("formatted as %q", s)
		}
	}
}

func TestErrorCodes(t *testing.T) {
	for code := CodeNotFound; code < numCodes; code++ {
		sentinel := sentinels[code]
		if sentinel == nil {
			t.Fatalf("code %d has no sentinel", code)
		}
		sent := Encode(fmt.Errorf("ytmusic: album %q: %w", "x", sentinel))
		if sent.Code != code {
			t.Errorf("Encode(%v) = code %d, want %d", sentinel, sent.Code, code)
		}
		got := roundTrip(t, &Frame{ID: 1, Reply: &Reply{Err: sent}})
		err := got.Reply.Err.Err()
		if !errors.Is(err, sentinel) {
			t.Errorf("code %d: %v is not %v", code, err, sentinel)
		}
		if !strings.HasPrefix(err.Error(), sentinel.Error()) {
			t.Errorf("code %d reads %q", code, err)
		}
	}

	// Invalid credentials keep their text for the sign-in form.
	invalid := fmt.Errorf("auth: no SAPISID cookie: %w", catalog.ErrInvalidCredentials)
	if got := Encode(invalid).Err().Error(); got != "catalog: invalid credentials: "+invalid.Error() {
		t.Errorf("invalid credentials read %q", got)
	}
	// Anything else says nothing of itself.
	if got := Encode(errors.New("GET https://secret.example/?key=1: EOF")); got.Code != CodeInternal || got.Detail != "" {
		t.Errorf("internal error encoded as %+v", got)
	}
	if !errors.Is(Encode(errors.New("x")).Err(), ErrInternal) {
		t.Error("an internal error does not read as ErrInternal")
	}
	// A newer daemon's code, or a missing one, reads as internal.
	for _, code := range []ErrorCode{0, numCodes, 999} {
		if err := (&Error{Code: code, Detail: "d"}).Err(); !errors.Is(err, ErrInternal) || err.Error() != ErrInternal.Error()+": d" {
			t.Errorf("code %d reads %v", code, err)
		}
	}
	if Encode(nil) != nil || (*Error)(nil).Err() != nil {
		t.Error("nil does not stay nil")
	}
}
