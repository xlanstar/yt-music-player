package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"

	"github.com/egoist/mygo/ui"
)

func signedOutApp(t *testing.T) (catalogtest.Accounts, *app, posts, *ui.Tester) {
	t.Helper()
	src, a, q := accountApp(t)
	return src, a, q, signedOut(t, src.Source, a, q)
}

// signedOut shows a after src answers that no one is signed in.
func signedOut(t *testing.T, src catalogtest.Source, a *app, q posts) *ui.Tester {
	t.Helper()
	tt := newTester(a)
	for _, c := range src.Take(t, tt.Frame, "account", "library", "playlist LM") {
		c.Reply <- reply{Err: catalog.ErrSignedOut}
	}
	settleLoads(t, tt, a, q)
	return tt
}

var (
	ada    = catalog.Account{Name: "Ada Lovelace", Handle: "@ada"}
	adaMix = catalog.Playlist{
		ID: "ada-mix", Name: "Engine Notes", Owner: "Ada Lovelace", Songs: []string{"far-1", "far-2"},
	}
	adaLibrary = catalog.Library{
		Playlists: []catalog.Playlist{adaMix}, Albums: []catalog.Album{remoteAlbum}, Songs: remoteTracks,
	}
	adaLikes = catalog.Playlist{ID: catalog.LikedID, Name: "Liked music", Songs: []string{"far-2", "far-1"}}
)

func signedInApp(t *testing.T) (catalogtest.Accounts, *app, posts, *ui.Tester) {
	t.Helper()
	src, a, q := accountApp(t)
	tt := newTester(a)
	calls := src.Take(t, tt.Frame, "account", "library", "playlist LM")
	calls["account"].Reply <- reply{Account: ada}
	calls["library"].Reply <- reply{Library: adaLibrary}
	calls["playlist LM"].Reply <- reply{Playlist: adaLikes, Songs: []catalog.Song{remoteTracks[1], remoteTracks[0]}}
	settleLoads(t, tt, a, q)
	return src, a, q, tt
}

// toasted reports whether any announcement, a toast's or not, contains s.
func toasted(tt *ui.Tester, s string) bool {
	return slices.ContainsFunc(tt.Announcements(), func(a string) bool { return strings.Contains(a, s) })
}

func TestSignedOut(t *testing.T) {
	src, a, _, tt := signedOutApp(t)
	if count(tt.Texts(), "Sign in") != 1 || tt.HasText("Ada") {
		t.Fatalf("the top bar does not offer to sign in, texts %q", tt.Texts())
	}
	// The placeholder's playlists are cached but in no one's library.
	if len(a.cat.LibraryPlaylists()) != 0 || tt.HasText("Late Night Drive") {
		t.Errorf("the sidebar lists playlists signed out, texts %q", tt.Texts())
	}
	a.router.Push(libraryPath(tabAlbums))
	tt.Frame()
	if !tt.HasText("Sign in to see your library") || tt.HasText("Velvet Static") {
		t.Errorf("library while signed out, texts %q", tt.Texts())
	}
	a.router.Push(pathLiked)
	tt.Frame()
	if !tt.HasText("Sign in to see your likes") || a.router.Title() != "Liked music" {
		t.Errorf("liked music while signed out, title %q, texts %q", a.router.Title(), tt.Texts())
	}

	// Liking signed out asks to sign in, and never asks the source.
	current := a.currentID()
	click(t, tt, "Like")
	if a.session.Liked(current) || !toasted(tt, "Sign in to like songs") {
		t.Errorf("liking signed out left liked %v, announced %q", a.session.Liked(current), tt.Announcements())
	}
	src.None(t, tt.Frame, 50*time.Millisecond)

	// The top bar's, the page's and the toast's.
	if count(tt.Texts(), "Sign in") != 3 {
		t.Fatalf("no Sign in on the page, texts %q", tt.Texts())
	}
	a.openSignIn()
	tt.Frame()
	if !tt.HasText("Sign in to YouTube Music") || !tt.Focused("Cookie header") {
		t.Errorf("no sign-in sheet with its field focused, texts %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if a.sheet.isOpen || tt.HasText("Sign in to YouTube Music") {
		t.Error("Escape left the sheet open")
	}
}

// A ytmd whose source signs no one in says so when asked to.
func TestSignedOutWithoutAccounts(t *testing.T) {
	src := catalogtest.New()
	a, q := sourceApp(t, src)
	fetchedFeeds(a)
	tt := signedOut(t, src, a, q)
	a.router.Push(pathLibrary)
	tt.Frame()
	if !tt.HasText("Sign in to see your library") {
		t.Errorf("texts %q", tt.Texts())
	}
	a.openSignIn()
	tt.Frame()
	tt.Type("SID=any")
	tt.Key(ui.Cmd, ui.KeyEnter)
	eventually(t, tt, q, "failed to sign in", func() bool { return a.sheet.failure != "" })
	if a.session.IsSignedIn() || !hasTextWith(tt, ytmdFailed) {
		t.Errorf("signed in without accounts, texts %q", tt.Texts())
	}
	src.None(t, tt.Frame, 50*time.Millisecond)
}

// hasTextWith reports whether any text shown contains s.
func hasTextWith(tt *ui.Tester, s string) bool {
	return slices.ContainsFunc(tt.Texts(), func(text string) bool { return strings.Contains(text, s) })
}

func TestSignIn(t *testing.T) {
	src, a, q, tt := signedOutApp(t)
	click(t, tt, "Sign in")
	if !tt.Focused("Cookie header") {
		t.Fatalf("the sheet's field is not focused, texts %q", tt.Texts())
	}
	// Tab is trapped in the sheet and reaches its button.
	tt.Type("Cookie: SID=bad;\n HSID=worse")
	for range 3 {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Sign in") {
			break
		}
	}
	if !tt.Focused("Sign in") {
		t.Fatal("Tab did not reach the sheet's Sign in")
	}
	tt.Key(0, ui.KeySpace)
	c := src.Next(t, tt.Frame, "signin", "SID=bad; HSID=worse")
	if a.sheet.credentials != "" || !tt.HasText("Signing in…") {
		t.Errorf("credentials %q kept while signing in, texts %q", a.sheet.credentials, tt.Texts())
	}
	answer(t, tt, q, c, reply{Err: fmt.Errorf("%w: these cookies are signed out", catalog.ErrInvalidCredentials)})
	showsErr := hasTextWith(tt, "these cookies are signed out") && toasted(tt, "these cookies are signed out")
	if !a.sheet.isOpen || !showsErr {
		t.Fatalf("no error, texts %q, announced %q", tt.Texts(), tt.Announcements())
	}
	if a.session.IsSignedIn() || count(tt.Texts(), "Sign in") != 2 {
		t.Errorf("a failed sign-in signed in, texts %q", tt.Texts())
	}

	click(t, tt, "Cookie header")
	tt.Type("SID=good")
	tt.Key(ui.Cmd, ui.KeyEnter)
	c = src.Next(t, tt.Frame, "signin", "SID=good")
	answer(t, tt, q, c, reply{Account: ada})
	if a.sheet.isOpen || tt.HasText("Cookie header") || !toasted(tt, "Signed in as Ada Lovelace") {
		t.Fatalf("the sheet stayed open, texts %q", tt.Texts())
	}
	if _, ok := tt.Find("Ada Lovelace"); !ok || count(tt.Texts(), "Sign in") != 0 {
		t.Errorf("the top bar does not show Ada, texts %q", tt.Texts())
	}
	// Signing in refetches the account's data, and Home.
	calls := src.Take(t, tt.Frame, "account", "library", "playlist LM", "home")
	calls["account"].Reply <- reply{Account: ada}
	calls["library"].Reply <- reply{Library: adaLibrary}
	calls["playlist LM"].Reply <- reply{Playlist: adaLikes, Songs: remoteTracks}
	calls["home"].Reply <- reply{}
	settleLoads(t, tt, a, q)
	if !tt.HasText("Engine Notes") || !a.session.Liked("far-1") || !a.session.Liked("far-2") {
		t.Errorf("Ada's library or likes missing, texts %q", tt.Texts())
	}
}

func TestSignInClosedWhileSigningIn(t *testing.T) {
	src, a, q, tt := signedOutApp(t)
	a.openSignIn()
	tt.Frame()
	tt.Type("SID=slow")
	tt.Key(ui.Cmd, ui.KeyEnter)
	c := src.Next(t, tt.Frame, "signin", "SID=slow")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.sheet.isOpen || a.session.IsSigningIn() {
		t.Fatal("closing the sheet left it signing in")
	}
	// ytmd's source answers all the same.
	c.Reply <- reply{Err: errors.New("these cookies are signed out")}
	runPosts(t, tt, q)
	src.None(t, tt.Frame, 50*time.Millisecond)
	if a.session.IsSignedIn() || a.sheet.failure != "" {
		t.Error("a sign-in given up on still told")
	}
}

func TestSignOut(t *testing.T) {
	src, a, q, tt := signedInApp(t)
	if !tt.HasText("Engine Notes") || !a.session.Liked("far-2") {
		t.Fatalf("not signed in, texts %q", tt.Texts())
	}
	click(t, tt, "Ada Lovelace")
	if m := tt.Menu(); !slices.Equal(m, []string{"Signed in as Ada Lovelace (@ada)", "-", "Sign out"}) {
		t.Fatalf("account menu %q", m)
	}
	if err := tt.ChooseMenuItem("Sign out"); err != nil {
		t.Fatal(err)
	}
	c := src.Next(t, tt.Frame, "signout", "")
	// Ada stays signed in until the source dropped the credentials.
	if !a.session.IsSignedIn() {
		t.Error("signed out before the source did")
	}
	answer(t, tt, q, c, reply{})
	src.Next(t, tt.Frame, "home", "").Reply <- reply{}
	settleLoads(t, tt, a, q)
	if a.session.IsSignedIn() || tt.HasText("Ada") || tt.HasText("Engine Notes") || count(tt.Texts(), "Sign in") != 1 {
		t.Errorf("Ada's account shows after signing out, texts %q", tt.Texts())
	}
	if a.session.Liked("far-2") || len(a.cat.LibraryPlaylists()) != 0 || len(a.session.Library().Songs) != 0 {
		t.Error("signing out kept likes or the library")
	}
	if !toasted(tt, "Signed out") {
		t.Errorf("announced %q", tt.Announcements())
	}
}

func TestSignOutFails(t *testing.T) {
	src, a, q, tt := signedInApp(t)
	click(t, tt, "Ada Lovelace")
	if err := tt.ChooseMenuItem("Sign out"); err != nil {
		t.Fatal(err)
	}
	answer(t, tt, q, src.Next(t, tt.Frame, "signout", ""), reply{Err: errors.New("keychain locked")})
	if !a.session.IsSignedIn() || !tt.HasText("Engine Notes") || !toasted(tt, ytmdFailed) {
		t.Errorf("a failed sign-out, texts %q, announced %q", tt.Texts(), tt.Announcements())
	}
}

func TestLibraryLoadsFailsAndRetries(t *testing.T) {
	src, a, q := accountApp(t)
	tt := newTester(a)
	a.router.Push(libraryPath(tabAlbums))
	calls := src.Take(t, tt.Frame, "account", "library", "playlist LM")
	answer(t, tt, q, calls["account"], reply{Account: ada})
	answer(t, tt, q, calls["playlist LM"], reply{Playlist: adaLikes, Songs: remoteTracks})
	if !tt.HasText("Loading…") || !tt.HasText("Albums") || tt.HasText("Velvet Static") {
		t.Fatalf("library not loading, texts %q", tt.Texts())
	}
	answer(t, tt, q, calls["library"], reply{Err: errors.New("network is down")})
	if !tt.HasText("Couldn’t load your library") || !tt.HasText(ytmdFailed) {
		t.Fatalf("no error, texts %q", tt.Texts())
	}
	// A failure is not retried behind the user's back.
	src.None(t, tt.Frame, 50*time.Millisecond)
	click(t, tt, "Retry")
	c := src.Next(t, tt.Frame, "library", "")
	if !tt.HasText("Loading…") {
		t.Errorf("retry not loading, texts %q", tt.Texts())
	}
	answer(t, tt, q, c, reply{Library: adaLibrary})
	// Only the library's albums: the placeholder's are cached, not saved.
	if !tt.HasText("Far Signals") || tt.HasText("Velvet Static") {
		t.Errorf("library albums wrong, texts %q", tt.Texts())
	}
	click(t, tt, "Songs")
	if !tt.HasText("Carrier Wave by Nova Static") || tt.HasText("Rewind by Kasette") {
		t.Errorf("library songs wrong, texts %q", tt.Texts())
	}
	// Fetched once per session.
	click(t, tt, "Home")
	click(t, tt, "Library")
	src.None(t, tt.Frame, 50*time.Millisecond)
}

func TestSidebarListsTheLibraryOnly(t *testing.T) {
	src, a, q, tt := signedInApp(t)
	// Playlists opened, searched or seen in a feed are cached, not saved.
	a.router.Push(playlistPath("remote-list"))
	answer(t, tt, q,
		src.Next(t, tt.Frame, "playlist", "remote-list"),
		reply{Playlist: remoteList, Songs: remoteTracks[:1]},
	)
	a.router.Push(albumPath("remote"))
	answer(t, tt, q, src.Next(t, tt.Frame, "album", "remote"), reply{Album: remoteAlbum, Songs: remoteTracks})
	if sidebar := libraryIDs(a.cat); !slices.Equal(sidebar, []string{"ada-mix"}) {
		t.Errorf("sidebar lists %q", sidebar)
	}
	a.router.Push(libraryPath(tabPlaylists))
	tt.Frame()
	// Label and text, in both the sidebar and the page.
	if count(tt.Texts(), "Static Hours") != 0 || count(tt.Texts(), "Engine Notes") != 4 {
		t.Errorf("library playlists wrong, texts %q", tt.Texts())
	}
	if !tt.HasText("Auto playlist · 2 songs") {
		t.Errorf("Liked music's count wrong, texts %q", tt.Texts())
	}
}

func TestLikedMusicSeedsLikes(t *testing.T) {
	src, a, q := accountApp(t)
	tt := newTester(a)
	a.router.Push(pathLiked)
	calls := src.Take(t, tt.Frame, "account", "library", "playlist LM")
	answer(t, tt, q, calls["account"], reply{Account: ada})
	if !tt.HasText("Loading…") || a.session.Liked("far-2") {
		t.Fatalf("liked music not loading, texts %q", tt.Texts())
	}
	// A like sent meanwhile is newer than the fetched list, so it stays.
	a.session.SetLiked("midnight-arcade", true)
	like := src.Next(t, tt.Frame, "like", "+midnight-arcade")
	answer(t, tt, q, calls["playlist LM"], reply{
		Playlist: adaLikes,
		Songs:    []catalog.Song{remoteTracks[1], remoteTracks[0]},
	})
	if !a.session.Liked("far-1") || !a.session.Liked("far-2") || !a.session.Liked("midnight-arcade") {
		t.Fatal("likes not seeded from Liked music")
	}
	if a.router.Title() != "Liked music" || !tt.HasText("Ada Lovelace") {
		t.Errorf("liked music's owner missing, texts %q", tt.Texts())
	}
	texts := tt.Texts()
	order := []int{
		slices.Index(texts, "Midnight Arcade by Lumen Drive"),
		slices.Index(texts, "Dead Air by Nova Static"),
		slices.Index(texts, "Carrier Wave by Nova Static"),
	}
	if order[0] < 0 || !slices.IsSorted(order) {
		t.Errorf("liked music not newest first, texts %q", texts)
	}
	like.Reply <- reply{}
	calls["library"].Reply <- reply{}
	settleLoads(t, tt, a, q)
	if !a.session.Liked("midnight-arcade") {
		t.Error("a like that went through was undone")
	}
}

func TestLikeRevertsOnError(t *testing.T) {
	src, a, q, tt := signedInApp(t)
	a.router.Push(pathLiked)
	tt.Frame()
	// Unliking removes the song from Liked music before the source answers.
	r, ok := tt.Find("Dead Air by Nova Static")
	if !ok {
		t.Fatalf("no Dead Air, texts %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	r, _ = tt.Find("Dead Air by Nova Static")
	tt.ClickAt(r.X+r.W+16+18, r.Y+r.H/2)
	c := src.Next(t, tt.Frame, "like", "-far-2")
	if a.session.Liked("far-2") || tt.HasText("Dead Air by Nova Static") {
		t.Fatalf("unliking waited for the source, texts %q", tt.Texts())
	}
	answer(t, tt, q, c, reply{Err: errors.New("quota exceeded")})
	frames(tt, 2)
	if !a.session.Liked("far-2") || !tt.HasText("Dead Air by Nova Static") {
		t.Errorf("a failed unlike stayed, texts %q", tt.Texts())
	}
	if !toasted(tt, "Couldn’t unlike “Dead Air”") || !tt.HasText(ytmdFailed) {
		t.Errorf("no error, texts %q, announced %q", tt.Texts(), tt.Announcements())
	}
	// Restored in its place, first.
	texts := tt.Texts()
	if slices.Index(texts, "Dead Air by Nova Static") > slices.Index(texts, "Carrier Wave by Nova Static") {
		t.Errorf("Dead Air moved, texts %q", texts)
	}
}
