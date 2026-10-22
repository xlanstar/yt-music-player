package remote

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/leakcheck"
	"yt-music-player/internal/notice"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// Only main sets the level: tests would otherwise print every failure
	// they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(leakcheck.Run(m))
}

// attach connects f's Client to d, as a Supervisor does, until the test
// ends.
func attach(t *testing.T, f *front, d *daemon) {
	t.Helper()
	conn := ipc.NewConn(d.connect(t.Context()))
	h, err := greet(t.Context(), conn, hello(api.RoleTUI, "1.0.0", true))
	if err != nil {
		t.Fatal(err)
	}
	f.client.open(conn, compare(h, "1.0.0"), false)
	t.Cleanup(func() {
		f.client.Close()
		d.exit()
	})
}

func ping(ctx context.Context, c *Client) error {
	_, err := c.Call(ctx, api.Call{Method: api.MethodPing})
	return err
}

func song(id string) catalog.Song { return catalog.Song{ID: id, Title: "Song " + id} }

func TestCallsNumberFromOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		results := catalog.Results{Songs: []catalog.Song{song("a")}}
		d.answer = func(*api.Call) *api.Reply { return &api.Reply{Results: &results} }
		f := newFront()
		attach(t, f, d)
		for range 2 {
			got, err := f.client.Search(t.Context(), "lofi")
			if err != nil || !reflect.DeepEqual(got, results) {
				t.Fatalf("Search = %+v, %v; want %+v", got, err, results)
			}
		}
		var ids []uint32
		for _, fr := range d.received() {
			ids = append(ids, fr.ID)
		}
		if !slices.Equal(ids, []uint32{1, 2}) {
			t.Errorf("IDs %v, want [1 2]", ids)
		}
	})
}

func TestSourceReplies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		album := catalog.Album{ID: "MPREb_a", Title: "Album"}
		songs := []catalog.Song{song("a"), song("b")}
		d.answer = func(c *api.Call) *api.Reply {
			switch c.Method {
			case api.MethodAlbum:
				return &api.Reply{Album: &api.AlbumReply{Album: album, Songs: songs}}
			case api.MethodSignIn:
				if c.SignIn.Credentials != "SID=x" {
					return &api.Reply{Err: &api.Error{Code: api.CodeInvalidCredentials, Detail: "no SAPISID"}}
				}
				return &api.Reply{Account: &catalog.Account{Name: "Lan"}}
			case api.MethodStatus:
				return &api.Reply{Status: &api.Status{Version: "1.0.0", Length: 2}}
			}
			return &api.Reply{}
		}
		f := newFront()
		attach(t, f, d)
		ctx := t.Context()

		if al, got, err := f.client.Album(ctx, album.ID); err != nil || al.ID != album.ID || len(got) != 2 {
			t.Errorf("Album = %+v, %d songs, %v", al, len(got), err)
		}
		if acct, err := f.client.SignIn(ctx, "SID=x"); err != nil || acct.Name != "Lan" {
			t.Errorf("SignIn = %+v, %v", acct, err)
		}
		_, err := f.client.SignIn(ctx, "nothing")
		if !errors.Is(err, catalog.ErrInvalidCredentials) || err.Error() != "catalog: invalid credentials: no SAPISID" {
			t.Errorf("SignIn with bad credentials: %v", err)
		}
		if st, err := f.client.Status(ctx); err != nil || st.Length != 2 {
			t.Errorf("Status = %+v, %v", st, err)
		}
		// A reply without the result its method gives.
		if _, err := f.client.Home(ctx); !errors.Is(err, api.ErrInvalid) {
			t.Errorf("Home without a result: %v", err)
		}
	})
}

func TestCallCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(*api.Call) *api.Reply { return nil }
		f := newFront()
		attach(t, f, d)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := f.client.Search(ctx, "lofi"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Search = %v, want the ctx's error", err)
		}
		synctest.Wait()
		got := d.received()
		if len(got) != 2 || got[1].ID != 1 || !got[1].Cancel {
			t.Errorf("daemon got %+v, want the call then its Cancel", got)
		}
	})
}

func TestDisconnectFailsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(*api.Call) *api.Reply { return nil }
		f := newFront()
		attach(t, f, d)
		errs := make(chan error, 2)
		go func() {
			_, err := f.client.Home(t.Context())
			errs <- err
		}()
		go func() {
			_, err := f.client.Search(t.Context(), "lofi")
			errs <- err
		}()
		synctest.Wait()
		d.exit()
		for range 2 {
			if err := <-errs; !errors.Is(err, api.ErrDisconnected) {
				t.Errorf("pending call = %v, want ErrDisconnected", err)
			}
		}
		if _, err := f.client.Explore(t.Context()); !errors.Is(err, api.ErrDisconnected) {
			t.Errorf("call after = %v, want ErrDisconnected", err)
		}
	})
}

// Go's callback runs before the events that followed its reply, and in
// order with them.
func TestGoKeepsOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(*api.Call) *api.Reply {
			return &api.Reply{Err: &api.Error{Code: api.CodeSignedOut}}
		}
		d.then = func(conn *ipc.Conn, _ *api.Call) {
			_ = conn.Send(api.Frame{Event: &api.Event{Account: &api.AccountEvent{}}})
		}
		f := newFront()
		attach(t, f, d)
		f.client.Go(t.Context(), api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: "a"}}, func(_ *api.Reply, err error) {
			if !errors.Is(err, catalog.ErrSignedOut) {
				t.Errorf("Like = %v, want ErrSignedOut", err)
			}
			f.events = append(f.events, "like")
		})
		synctest.Wait()
		f.ui.run()
		if want := []string{"like", "account"}; !slices.Equal(f.events, want) {
			t.Errorf("ran %v, want %v", f.events, want)
		}
	})
}

func TestGoCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(*api.Call) *api.Reply { return nil }
		f := newFront()
		attach(t, f, d)
		ctx, cancel := context.WithCancel(t.Context())
		var got []error
		f.client.Go(ctx, api.Call{Method: api.MethodHome}, func(_ *api.Reply, err error) { got = append(got, err) })
		synctest.Wait()
		cancel()
		synctest.Wait()
		f.ui.run()
		if len(got) != 1 || !errors.Is(got[0], context.Canceled) {
			t.Errorf("done got %v, want one context.Canceled", got)
		}
		if fr := d.received(); len(fr) != 2 || !fr[1].Cancel {
			t.Errorf("daemon got %+v, want the call then its Cancel", fr)
		}
		// Without a connection, done hears of it.
		f.client.Close()
		f.client.Go(t.Context(), api.Call{Method: api.MethodHome}, func(_ *api.Reply, err error) { got = append(got, err) })
		f.ui.run()
		if len(got) != 2 || !errors.Is(got[1], api.ErrDisconnected) {
			t.Errorf("done got %v, want ErrDisconnected last", got)
		}
	})
}

func TestControls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		f := newFront()
		attach(t, f, d)
		songs := []catalog.Song{song("a"), song("b")}
		c := f.client
		c.Play([]string{"a", "b"}, 1, songs)
		c.Next()
		c.Seek(12.5)
		c.SetVolume(0.5)
		c.Enqueue([]string{"b"}, true, songs[1:])
		c.Move(1, 0)
		c.Remove(1)
		synctest.Wait()
		f.ui.run()
		if len(f.notices) != 0 {
			t.Errorf("notices %+v, want none", f.notices)
		}
		want := []api.Method{
			api.MethodPlay, api.MethodNext, api.MethodSeek, api.MethodSetVolume,
			api.MethodEnqueue, api.MethodMove, api.MethodRemove,
		}
		if got := d.calls(); !slices.Equal(got, want) {
			t.Fatalf("daemon got %v, want %v (in order)", got, want)
		}
		frames := d.received()
		play := frames[0].Call.IDs
		if play.Start != 1 || !reflect.DeepEqual(play.Songs, songs) {
			t.Errorf("Play's args %+v", play)
		}
		if enq := frames[4].Call.IDs; !enq.Next || len(enq.Songs) != 1 {
			t.Errorf("Enqueue's args %+v", enq)
		}
	})
}

func TestControlFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(c *api.Call) *api.Reply {
			if c.Method == api.MethodPause {
				return &api.Reply{Err: &api.Error{Code: api.CodeInternal}}
			}
			return &api.Reply{Err: &api.Error{Code: api.CodeNotFound, Detail: "no such song"}}
		}
		f := newFront()
		attach(t, f, d)
		f.client.Pause()
		f.client.Play([]string{"x"}, 0, nil)
		synctest.Wait()
		if len(f.notices) != 0 {
			t.Fatal("told before the front-end's thread ran")
		}
		f.ui.run()
		want := []notice.Notice{
			{Kind: notice.KindFailure, Title: notice.ControlFailed, Topic: notice.TopicControl},
			{
				Kind: notice.KindFailure, Title: notice.ControlFailed, Detail: notice.Reason,
				Args: []string{"no such song"}, Topic: notice.TopicControl,
			},
		}
		if !reflect.DeepEqual(f.notices, want) {
			t.Errorf("notices %+v, want %+v", f.notices, want)
		}
	})
}

func TestControlTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		d.answer = func(*api.Call) *api.Reply { return nil }
		f := newFront()
		attach(t, f, d)
		f.client.Next()
		time.Sleep(controlTimeout - time.Millisecond)
		synctest.Wait()
		f.ui.run()
		if len(f.notices) != 0 {
			t.Fatalf("told before %v", controlTimeout)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		f.ui.run()
		if len(f.notices) != 1 || f.notices[0].Title != notice.ControlFailed {
			t.Errorf("notices %+v, want ControlFailed", f.notices)
		}
		if got := d.received(); len(got) != 2 || !got[1].Cancel || got[1].ID != 1 {
			t.Errorf("daemon got %+v, want Next then its Cancel", got)
		}
	})
}

func TestControlDisconnected(t *testing.T) {
	f := newFront()
	f.client.Toggle()
	f.ui.run()
	if len(f.notices) != 1 || f.notices[0].Title != notice.ControlFailed {
		t.Errorf("notices %+v, want ControlFailed", f.notices)
	}
}

// A burst of events costs one post; the Mirror and the Handler get them
// in order, the account before what it replaces.
func TestEventsBatched(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		f := newFront()
		attach(t, f, d)
		d.send(api.Frame{Event: &api.Event{
			State: &api.State{Current: "a", Playing: true},
			Queue: &api.Queue{IDs: []string{"a", "b"}},
			Songs: []catalog.Song{song("a"), song("b")},
		}})
		d.send(api.Frame{Event: &api.Event{
			Likes:   &api.Likes{Full: true},
			Library: &catalog.Library{},
			Account: &api.AccountEvent{SignedIn: true},
		}})
		d.send(api.Frame{Event: &api.Event{Failed: &api.FailedEvent{Part: api.PartLiked}}})
		d.send(api.Frame{Event: &api.Event{Notice: &notice.Notice{Title: notice.Skipped}}})
		d.send(api.Frame{Event: &api.Event{State: &api.State{Current: "b", Index: 1}}})
		synctest.Wait()
		if f.ui.posts != 1 {
			t.Errorf("%d posts, want 1", f.ui.posts)
		}
		f.ui.run()
		m := f.client.Mirror()
		if st := m.State(); st.Current != "b" || st.Playing {
			t.Errorf("State %+v, want the last", st)
		}
		if _, ok := m.Song("b"); !ok || !slices.Equal(m.Queue(), []string{"a", "b"}) {
			t.Errorf("queue %v, without song b", m.Queue())
		}
		if want := []string{"account", "library", "likes", "failed"}; !slices.Equal(f.events, want) {
			t.Errorf("handler got %v, want %v", f.events, want)
		}
		if len(f.notices) != 1 || f.notices[0].Title != notice.Skipped {
			t.Errorf("notices %+v", f.notices)
		}
	})
}

// After a reconnection, the snapshot replaces the Mirror.
func TestMirrorReplacedAfterReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFront()
		first := newDaemon("1.0.0")
		attach(t, f, first)
		first.send(api.Frame{Event: &api.Event{
			Queue: &api.Queue{IDs: []string{"a", "b"}},
			Songs: []catalog.Song{song("a"), song("b")},
		}})
		synctest.Wait()
		f.ui.run()
		first.exit()
		synctest.Wait()

		second := newDaemon("1.0.0")
		attach(t, f, second)
		second.send(api.Frame{Event: &api.Event{State: &api.State{Current: "c"}}})
		second.send(api.Frame{Event: &api.Event{
			Queue: &api.Queue{IDs: []string{"c"}},
			Songs: []catalog.Song{song("c")},
		}})
		synctest.Wait()
		gen := f.client.Mirror().QueueGen()
		f.ui.run()
		m := f.client.Mirror()
		if _, ok := m.Song("a"); ok {
			t.Error("the first daemon's song stays")
		}
		if _, ok := m.Song("c"); !ok || m.State().Current != "c" || !slices.Equal(m.Queue(), []string{"c"}) {
			t.Errorf("State %+v, queue %v", m.State(), m.Queue())
		}
		if m.QueueGen() == gen {
			t.Error("QueueGen unchanged")
		}
	})
}

// A reconnection's songs come ahead of its queue, in events of their own:
// till the queue, the one before shows with its songs.
func TestMirrorSongsAhead(t *testing.T) {
	var m Mirror
	at := time.Now()
	m.apply(&api.Event{Queue: &api.Queue{IDs: []string{"a"}}, Songs: []catalog.Song{song("a")}}, 1, at)
	m.apply(&api.Event{Songs: []catalog.Song{song("b")}}, 2, at)
	m.apply(&api.Event{Songs: []catalog.Song{song("c")}}, 2, at)
	if _, ok := m.Song("a"); !ok || !slices.Equal(m.Queue(), []string{"a"}) {
		t.Errorf("queue %v, song a gone before the next queue", m.Queue())
	}
	m.apply(&api.Event{Queue: &api.Queue{IDs: []string{"b", "c"}}}, 2, at)
	if _, ok := m.Song("a"); ok {
		t.Error("the first link's song stays")
	}
	for _, id := range m.Queue() {
		if _, ok := m.Song(id); !ok {
			t.Errorf("song %s missing", id)
		}
	}

	// The same queue again, a song described anew.
	retitled := catalog.Song{ID: "b", Title: "New"}
	m.apply(&api.Event{Songs: []catalog.Song{retitled, song("c")}}, 3, at)
	m.apply(&api.Event{Queue: &api.Queue{IDs: []string{"b", "c"}}}, 3, at)
	if b, _ := m.Song("b"); b != retitled || len(m.songs) != 2 {
		t.Errorf("song b %+v, %d songs", b, len(m.songs))
	}
}

func TestMirrorPrunes(t *testing.T) {
	var m Mirror
	at := time.Now()
	for i := range 200 {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		m.apply(&api.Event{Queue: &api.Queue{IDs: []string{id}}, Songs: []catalog.Song{song(id)}}, 1, at)
	}
	if len(m.songs) > 2*1+64 {
		t.Errorf("%d songs kept for a queue of 1", len(m.songs))
	}
	if _, ok := m.Song(m.Queue()[0]); !ok {
		t.Error("the queued song is gone")
	}
}

func TestPosition(t *testing.T) {
	at := time.Now()
	later := at.Add(10 * time.Second)
	for _, tc := range []struct {
		name  string
		state api.State
		want  float64
	}{
		{"playing", api.State{Playing: true, Position: 5, Duration: 200}, 15},
		{"paused", api.State{Position: 5, Duration: 200}, 5},
		{"waiting", api.State{Playing: true, Waiting: true, Position: 5, Duration: 200}, 5},
		{"at the end", api.State{Playing: true, Position: 195, Duration: 200}, 200},
		{"no duration", api.State{Playing: true, Position: 5}, 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Position(tc.state, at, later); got != tc.want {
				t.Errorf("Position = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDaemon("1.0.0")
		dl := &dialer{}
		if _, _, err := Connect(t.Context(), dl, false, api.RoleCLI, "1.2.0"); err == nil {
			t.Fatal("Connect without a daemon succeeded")
		}
		if dl.count("spawn") != 0 {
			t.Error("Connect without spawn started ytmd")
		}
		dl.next = []*daemon{d}
		c, peer, err := Connect(t.Context(), dl, true, api.RoleCLI, "1.2.0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer d.exit()
		if !peer.IsOlder || peer.IsNewer || !peer.IsCompatible || peer.Hello.PID != daemonPID {
			t.Errorf("peer %+v, want an older, compatible daemon", peer)
		}
		if h := d.greetings(); len(h) != 1 || h[0].Subscribe || h[0].Role != api.RoleCLI {
			t.Errorf("hello %+v, want a CLI's, not subscribing", h)
		}
		if err := ping(t.Context(), c); err != nil {
			t.Error(err)
		}
		if err := c.Shutdown(t.Context()); err != nil {
			t.Error(err)
		}
		if got := d.calls(); !slices.Equal(got, []api.Method{api.MethodPing, api.MethodShutdown}) {
			t.Errorf("calls %v; never a hand-off", got)
		}
	})
}
