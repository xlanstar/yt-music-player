package ytmusic

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/auth"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/innertube"
)

// SAPISIDs the test server knows: a live session, an expired one
// (answered as to a visitor, as InnerTube does), and anything else,
// refused with a 401.
const (
	goodSAPISID    = "fakeSAPISID/good"
	expiredSAPISID = "fakeSAPISID/expired"
	testCookie     = "VISITOR_INFO1_LIVE=fakevisitor; SID=fakesid; SAPISID=" + goodSAPISID +
		"; __Secure-3PAPISID=" + goodSAPISID
	expiredCookie = "SID=fakesid; SAPISID=" + expiredSAPISID
	wrongCookie   = "SID=fakesid; SAPISID=fakeSAPISID/wrong"
)

// signedInRoutes serve a live session from hand-written fixtures:
// signed-in responses hold personal data, so none is recorded (see
// recordLibrary).
var signedInRoutes = map[string]string{
	`account/account_menu {}`:                                    "account_menu.json",
	`browse {"browseId":"FEmusic_liked_playlists"}`:              "liked_playlists.json",
	`browse {"continuation":"FAKE_PLAYLISTS_CONTINUATION"}`:      "liked_playlists_continuation.json",
	`browse {"browseId":"FEmusic_liked_albums"}`:                 "liked_albums.json",
	`browse {"browseId":"FEmusic_library_corpus_track_artists"}`: "library_artists.json",
	`browse {"continuation":"FAKE_ARTISTS_CONTINUATION"}`:        "library_artists_continuation.json",
	`browse {"browseId":"FEmusic_liked_videos"}`:                 "library_songs.json",
	`browse {"continuation":"FAKE_SONGS_CONTINUATION"}`:          "library_songs_continuation.json",
	`browse {"browseId":"VLLM"}`:                                 "liked_music.json",
	`browse {"continuation":"FAKE_LIKED_CONTINUATION"}`:          "liked_music_continuation.json",
	`like/like {"target":{"videoId":"fakevideo01"}}`:             "like.json",
	`like/removelike {"target":{"videoId":"fakevideo01"}}`:       "like.json",
}

type session int

const (
	anonymous session = iota
	live
	expired
	refused
)

type served struct {
	key     string
	session session
}

// ytmServer answers signed-in requests from signedInRoutes, signed-out
// ones as InnerTube does, and public ones from the recorded fixtures. It
// checks the headers of every signed request.
type ytmServer struct {
	t      *testing.T
	public http.HandlerFunc
	mu     sync.Mutex
	log    []served
	// fail maps a request key to the error status to answer instead.
	fail map[string]int
}

func (s *ytmServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(data))
	endpoint, body := readRequest(s.t, r)
	r.Body = io.NopCloser(bytes.NewReader(data))
	key := requestKey(endpoint, body)
	sess := s.session(r)
	s.mu.Lock()
	s.log = append(s.log, served{key, sess})
	status := s.fail[key]
	s.mu.Unlock()

	file, signedOnly := signedInRoutes[key]
	switch {
	case status != 0:
		http.Error(w, `{"error":{"message":"injected failure"}}`, status)
	case sess == refused:
		http.Error(w, `{"error":{"code":401,"message":"Request had invalid authentication credentials."}}`,
			http.StatusUnauthorized)
	case !signedOnly:
		s.public(w, r)
	case sess == live:
		http.ServeFile(w, r, filepath.Join("testdata", "signed_in", file))
	case strings.HasPrefix(endpoint, "like/"):
		http.Error(w, `{"error":{"code":401,"message":"You must be signed in to perform this operation."}}`,
			http.StatusUnauthorized)
	case endpoint == "account/account_menu":
		http.ServeFile(w, r, filepath.Join("testdata", "signed_in", "account_menu_signed_out.json"))
	default:
		http.ServeFile(w, r, filepath.Join("testdata", "signed_in", "signed_out_page.json"))
	}
}

// session checks a request's credentials as InnerTube would, and that
// the decorator sent all it must.
func (s *ytmServer) session(r *http.Request) session {
	t := s.t
	header := r.Header
	authz := header.Get("Authorization")
	cookie := header.Get("Cookie")
	if authz == "" {
		if strings.Contains(cookie, "SAPISID") || header.Get("X-Goog-AuthUser") != "" {
			t.Errorf("unsigned request carries credentials: %v", header)
		}
		return anonymous
	}
	var sapisid string
	for part := range strings.SplitSeq(cookie, ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "SAPISID="); ok {
			sapisid = v
		}
	}
	tsText, _, _ := strings.Cut(strings.TrimPrefix(authz, "SAPISIDHASH "), "_")
	ts, err := strconv.ParseInt(tsText, 10, 64)
	if err != nil || authz != auth.SAPISIDHash(sapisid, innertube.Origin, time.Unix(ts, 0)) {
		t.Errorf("Authorization %q does not match the cookie's SAPISID", authz)
	}
	if d := time.Since(time.Unix(ts, 0)); d < -time.Minute || d > time.Minute {
		t.Errorf("SAPISIDHASH stamped %v ago", d)
	}
	hasHeaders := header.Get("X-Origin") == innertube.Origin && header.Get("X-Goog-AuthUser") != "" &&
		strings.Contains(cookie, "SOCS=CAI")
	if !hasHeaders {
		t.Errorf("signed request lacks headers: X-Origin %q, X-Goog-AuthUser %q, Cookie has SOCS %v",
			header.Get("X-Origin"), header.Get("X-Goog-AuthUser"), strings.Contains(cookie, "SOCS="))
	}
	switch sapisid {
	case goodSAPISID:
		return live
	case expiredSAPISID:
		return expired
	}
	return refused
}

func (s *ytmServer) served() []served {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

func (s *ytmServer) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = nil
}

func (s *ytmServer) failWith(key string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail == nil {
		s.fail = map[string]int{}
	}
	s.fail[key] = status
}

func accountClient(t *testing.T, store auth.Store) (*Client, *ytmServer) {
	t.Helper()
	s := &ytmServer{t: t, public: fixtureHandler(t)}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	api := &innertube.Client{HTTP: srv.Client(), BaseURL: srv.URL + "/youtubei/v1/", HL: "en", Retries: -1}
	return New(api, store), s
}

func signedIn(t *testing.T, store auth.Store) (*Client, *ytmServer) {
	t.Helper()
	c, s := accountClient(t, store)
	if _, err := c.SignIn(t.Context(), "Cookie: "+testCookie); err != nil {
		t.Fatal(err)
	}
	s.reset()
	return c, s
}

func mustParse(t *testing.T, s string) auth.Credentials {
	t.Helper()
	c, err := auth.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var jane = catalog.Account{
	Name:   "Jane Placeholder",
	Handle: "@janeplaceholder",
	Photo:  "https://yt3.ggpht.com/fake-photo=s176-c-k-c0x00ffffff-no-rj",
}

func TestSignIn(t *testing.T) {
	store := &auth.MemoryStore{}
	c, s := accountClient(t, store)
	// Chrome's request headers, as copied from DevTools.
	headers := ":authority: music.youtube.com\naccept: */*\ncookie: " + testCookie + "\nx-goog-authuser: 0\n"
	acct, err := c.SignIn(t.Context(), headers)
	if err != nil {
		t.Fatal(err)
	}
	if acct != jane {
		t.Errorf("SignIn = %+v, want %+v", acct, jane)
	}
	if got, err := store.Load(); err != nil || got != mustParse(t, testCookie) {
		t.Errorf("stored %v, %v", got, err)
	}
	if acct, err := c.Account(t.Context()); err != nil || acct != jane {
		t.Errorf("Account = %+v, %v", acct, err)
	}
	want := []served{{`account/account_menu {}`, live}, {`account/account_menu {}`, live}}
	if got := s.served(); !slices.Equal(got, want) {
		t.Errorf("served %v, want %v", got, want)
	}
}

func TestSignInFailureKeepsAccount(t *testing.T) {
	store := &auth.MemoryStore{}
	c, s := signedIn(t, store)
	good := mustParse(t, testCookie)

	for _, tc := range []struct {
		name, creds string
		want        error
		requests    int
	}{
		{"garbage", "hello", auth.ErrInvalid, 0},
		{"no SAPISID", "Cookie: SID=x; HSID=y", auth.ErrInvalid, 0},
		{"refused", wrongCookie, errRejected, 1},
		{"expired", expiredCookie, errRejected, 1},
	} {
		s.reset()
		_, err := c.SignIn(t.Context(), tc.creds)
		if !errors.Is(err, tc.want) || errors.Is(err, catalog.ErrSignedOut) {
			t.Errorf("%s: err = %v, want %v and not ErrSignedOut", tc.name, err, tc.want)
		}
		if err != nil && (strings.Contains(err.Error(), "fakeSAPISID") || strings.Contains(err.Error(), "fakesid")) {
			t.Errorf("%s: err leaks the credentials: %v", tc.name, err)
		}
		if n := len(s.served()); n != tc.requests {
			t.Errorf("%s: %d requests, want %d", tc.name, n, tc.requests)
		}
		if got, _ := store.Load(); got != good {
			t.Errorf("%s: the store lost the account", tc.name)
		}
		if acct, err := c.Account(t.Context()); err != nil || acct != jane {
			t.Errorf("%s: Account = %+v, %v; want the old account", tc.name, acct, err)
		}
	}

	// A network failure is no verdict on the credentials.
	s.failWith(`account/account_menu {}`, http.StatusServiceUnavailable)
	if _, err := c.SignIn(t.Context(), expiredCookie); err == nil || errors.Is(err, errRejected) {
		t.Errorf("503: err = %v, want a plain error", err)
	}
	if got, _ := store.Load(); got != good {
		t.Error("503: the store lost the account")
	}
}

type failingStore struct{ auth.MemoryStore }

var errDisk = errors.New("disk full")

func (*failingStore) Save(auth.Credentials) error { return errDisk }
func (*failingStore) Clear() error                { return errDisk }

func TestSignInStoreFailure(t *testing.T) {
	c, _ := accountClient(t, &failingStore{})
	if _, err := c.SignIn(t.Context(), testCookie); !errors.Is(err, errDisk) {
		t.Errorf("err = %v, want the store's", err)
	}
	if _, err := c.Account(t.Context()); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("Account after a failed save: err = %v, want ErrSignedOut", err)
	}
}

func TestSignOut(t *testing.T) {
	store := &auth.MemoryStore{}
	c, s := signedIn(t, store)
	if err := c.SignOut(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, auth.ErrNotStored) {
		t.Errorf("store after SignOut: err = %v, want ErrNotStored", err)
	}
	if _, err := c.Account(t.Context()); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("Account: err = %v, want ErrSignedOut", err)
	}
	if _, err := c.Search(t.Context(), searchQuery); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.served() {
		if r.session != anonymous {
			t.Errorf("signed request after SignOut: %v", r)
		}
	}
}

func TestSignOutStoreFailure(t *testing.T) {
	c, _ := accountClient(t, &failingStore{})
	// SignIn cannot save to this store, so sign in directly.
	c.api.Auth.Set(mustParse(t, testCookie).Decorator(nil))
	if err := c.SignOut(); !errors.Is(err, errDisk) {
		t.Errorf("err = %v, want the store's", err)
	}
	if c.api.Auth.IsSignedIn() {
		t.Error("still signed in after SignOut")
	}
}

func TestStoredCredentials(t *testing.T) {
	store := &auth.MemoryStore{}
	if err := store.Save(mustParse(t, testCookie)); err != nil {
		t.Fatal(err)
	}
	c, s := accountClient(t, store)
	if len(s.served()) != 0 {
		t.Error("New made requests")
	}
	if acct, err := c.Account(t.Context()); err != nil || acct != jane {
		t.Errorf("Account = %+v, %v", acct, err)
	}
}

type brokenStore struct{ auth.MemoryStore }

func (*brokenStore) Load() (auth.Credentials, error) { return auth.Credentials{}, errDisk }

func TestUnreadableStoreStartsSignedOut(t *testing.T) {
	c, s := accountClient(t, &brokenStore{})
	if _, err := c.Account(t.Context()); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("err = %v, want ErrSignedOut", err)
	}
	if len(s.served()) != 0 {
		t.Error("signed-out Account made a request")
	}
}

func TestSignedOutMakesNoRequest(t *testing.T) {
	c, s := accountClient(t, nil)
	ctx := t.Context()
	_, accountErr := c.Account(ctx)
	_, libraryErr := c.Library(ctx)
	likeErr := c.Like(ctx, "fakevideo01", true)
	_, _, likedErr := c.Playlist(ctx, catalog.LikedID)
	for name, err := range map[string]error{
		"Account": accountErr,
		"Library": libraryErr,
		"Like":    likeErr,
		"Liked":   likedErr,
	} {
		if !errors.Is(err, catalog.ErrSignedOut) {
			t.Errorf("%s: err = %v, want ErrSignedOut", name, err)
		}
	}
	if got := s.served(); len(got) != 0 {
		t.Errorf("signed-out calls made requests: %v", got)
	}
}

func TestPublicCallsSendCredentials(t *testing.T) {
	c, s := signedIn(t, nil)
	res, err := c.Search(t.Context(), searchQuery)
	if err != nil || len(res.Songs) == 0 {
		t.Fatalf("Search = %d songs, %v", len(res.Songs), err)
	}
	if _, err := c.Home(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := s.served()
	if len(got) < 3 {
		t.Fatalf("served %v", got)
	}
	for _, r := range got {
		if r.session != live {
			t.Errorf("public call not signed: %v", r)
		}
	}
}

func TestRefusedCredentialsRetryAnonymously(t *testing.T) {
	store := &auth.MemoryStore{}
	if err := store.Save(mustParse(t, wrongCookie)); err != nil {
		t.Fatal(err)
	}
	c, s := accountClient(t, store)
	if _, _, err := c.Album(t.Context(), albumID); err != nil {
		t.Fatal(err)
	}
	key := `browse {"browseId":"` + albumID + `"}`
	want := []served{{key, refused}, {key, anonymous}}
	if got := s.served(); !slices.Equal(got, want) {
		t.Errorf("served %v, want %v", got, want)
	}
	// Calls that need the account report the refusal instead.
	_, err := c.Account(t.Context())
	if !errors.Is(err, catalog.ErrSignedOut) || !errors.Is(err, innertube.ErrUnauthorized) {
		t.Errorf("Account: err = %v, want ErrSignedOut", err)
	}
}

func TestExpiredSession(t *testing.T) {
	store := &auth.MemoryStore{}
	if err := store.Save(mustParse(t, expiredCookie)); err != nil {
		t.Fatal(err)
	}
	c, _ := accountClient(t, store)
	ctx := t.Context()
	if _, err := c.Account(ctx); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("Account: err = %v, want ErrSignedOut", err)
	}
	if _, err := c.Library(ctx); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("Library: err = %v, want ErrSignedOut", err)
	}
	if _, _, err := c.Playlist(ctx, catalog.LikedID); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("Liked: err = %v, want ErrSignedOut", err)
	}
	if err := c.Like(ctx, "fakevideo01", true); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("Like: err = %v, want ErrSignedOut", err)
	}
}

func TestSignedOutResponse(t *testing.T) {
	if !signedOut(decoded(t, filepath.Join("signed_in", "signed_out_page.json"))) {
		t.Error("sign-in prompt not seen as signed out")
	}
	for _, tc := range []struct {
		json string
		want bool
	}{
		{`{"responseContext":{"serviceTrackingParams":[{"service":"GFEEDBACK",` +
			`"params":[{"key":"e","value":"1"},{"key":"logged_in","value":"0"}]}]}}`, true},
		{`{"responseContext":{"serviceTrackingParams":[{"service":"GFEEDBACK",` +
			`"params":[{"key":"logged_in","value":"1"}]}]}}`, false},
		{`{"contents":{}}`, false},
	} {
		resp, err := decode([]byte(tc.json))
		if err != nil {
			t.Fatal(err)
		}
		if got := signedOut(resp); got != tc.want {
			t.Errorf("signedOut(%s) = %v", tc.json, got)
		}
	}
}

func TestLike(t *testing.T) {
	c, s := signedIn(t, nil)
	if err := c.Like(t.Context(), "fakevideo01", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Like(t.Context(), "fakevideo01", false); err != nil {
		t.Fatal(err)
	}
	want := []served{
		{`like/like {"target":{"videoId":"fakevideo01"}}`, live},
		{`like/removelike {"target":{"videoId":"fakevideo01"}}`, live},
	}
	if got := s.served(); !slices.Equal(got, want) {
		t.Errorf("served %v, want %v", got, want)
	}
	if err := c.Like(t.Context(), "", true); err == nil {
		t.Error("Like of no song succeeded")
	}
	s.failWith(`like/like {"target":{"videoId":"fakevideo01"}}`, http.StatusInternalServerError)
	if err := c.Like(t.Context(), "fakevideo01", true); err == nil || errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("500: err = %v", err)
	}
}

func TestLikedPlaylist(t *testing.T) {
	c, s := signedIn(t, nil)
	pl, songs, err := c.Playlist(t.Context(), catalog.LikedID)
	if err != nil {
		t.Fatal(err)
	}
	if pl.ID != catalog.LikedID || pl.Name != "Liked music" || pl.Owner != "Jane Placeholder" || pl.Art == "" {
		t.Errorf("playlist = %+v", pl)
	}
	if !slices.Equal(pl.Songs, []string{"fakevideo02", "fakevideo01"}) || len(songs) != 2 {
		t.Fatalf("songs = %v", pl.Songs)
	}
	want := catalog.Song{
		ID: "fakevideo02", Title: "Placeholder Anthem", Artist: "Example Singer",
		Album: "Imaginary EP", AlbumID: "MPREb_fakealbum0002", Seconds: 241,
		Art: "https://lh3.googleusercontent.com/fake-fakevideo02=w120-h120-l90-rj",
	}
	if songs[0] != want {
		t.Errorf("song 0 = %+v, want %+v", songs[0], want)
	}
	wantServed := []served{
		{`browse {"browseId":"VLLM"}`, live},
		{`browse {"continuation":"FAKE_LIKED_CONTINUATION"}`, live},
	}
	if got := s.served(); !slices.Equal(got, wantServed) {
		t.Errorf("served %v, want %v", got, wantServed)
	}
	// "VLLM" names it too.
	if pl, _, err := c.Playlist(t.Context(), "VL"+catalog.LikedID); err != nil || pl.ID != catalog.LikedID {
		t.Errorf("Playlist(VLLM) = %q, %v", pl.ID, err)
	}
}

// TestConcurrentSwap is meant for -race.
func TestConcurrentSwap(t *testing.T) {
	c, _ := accountClient(t, &auth.MemoryStore{})
	ctx := t.Context()
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 10 {
			if _, err := c.SignIn(ctx, testCookie); err != nil {
				t.Error(err)
			}
			if err := c.SignOut(); err != nil {
				t.Error(err)
			}
		}
	})
	for range 4 {
		wg.Go(func() {
			for range 5 {
				if _, err := c.Account(ctx); err != nil && !errors.Is(err, catalog.ErrSignedOut) {
					t.Error(err)
				}
				if _, err := c.Library(ctx); err != nil && !errors.Is(err, catalog.ErrSignedOut) {
					t.Error(err)
				}
				if _, _, err := c.Album(ctx, albumID); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}
