package daemon

import (
	"errors"
	"slices"
	"testing"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
)

// methodStep is a call and the proof that it was served: what it asked of
// the source (serve), its reply, an event it caused, or the Service stopped.
type methodStep struct {
	call  api.Call
	serve func(h *harness)
	reply func(api.Reply) bool
	event func(api.Event) bool
	stops bool // run on a Service of its own
}

func stateIs(want func(api.State) bool) func(api.Event) bool {
	return func(ev api.Event) bool { return ev.State != nil && want(*ev.State) }
}

func queueIs(want ...string) func(api.Event) bool {
	return func(ev api.Event) bool { return ev.Queue != nil && slices.Equal(ev.Queue.IDs, want) }
}

func answer(kind, id string, r catalogtest.Reply) func(h *harness) {
	return func(h *harness) { h.src.Next(h.t, nil, kind, id).Reply <- r }
}

func noError(r api.Reply) bool { return r.Err == nil }

// methodSteps serve every Method, in this order, on one Service.
var methodSteps = []methodStep{
	// Its answer is all a Ping does.
	{call: api.Call{Method: api.MethodPing}, reply: noError},
	{call: api.Call{Method: api.MethodStatus}, reply: func(r api.Reply) bool { return r.Status != nil }},
	{
		call:  playAll(songs(4), 1),
		event: stateIs(func(st api.State) bool { return st.Current == "s1" && st.Playing }),
	},
	{call: api.Call{Method: api.MethodToggle}, event: stateIs(func(st api.State) bool { return !st.Playing })},
	{call: api.Call{Method: api.MethodResume}, event: stateIs(func(st api.State) bool { return st.Playing })},
	{call: api.Call{Method: api.MethodPause}, event: stateIs(func(st api.State) bool { return !st.Playing })},
	{call: api.Call{Method: api.MethodNext}, event: stateIs(func(st api.State) bool { return st.Current == "s2" })},
	{call: api.Call{Method: api.MethodPrev}, event: stateIs(func(st api.State) bool { return st.Current == "s1" })},
	{
		call:  api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: 42}},
		event: stateIs(func(st api.State) bool { return st.Position == 42 }),
	},
	{
		call:  api.Call{Method: api.MethodSetVolume, Volume: &api.VolumeArgs{Volume: 0.25}},
		event: stateIs(func(st api.State) bool { return st.Volume == 0.25 }),
	},
	{call: api.Call{Method: api.MethodToggleMute}, event: stateIs(func(st api.State) bool { return st.Muted })},
	{
		call:  api.Call{Method: api.MethodCycleRepeat},
		event: stateIs(func(st api.State) bool { return st.Repeat == api.RepeatAll }),
	},
	{
		call: api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{
			IDs: []string{"e1"}, Songs: []catalog.Song{{ID: "e1"}}, Next: true,
		}},
		event: queueIs("s0", "s1", "e1", "s2", "s3"),
	},
	{
		call:  api.Call{Method: api.MethodMove, Move: &api.MoveArgs{From: 2, To: 4}},
		event: queueIs("s0", "s1", "s2", "s3", "e1"),
	},
	{
		call:  api.Call{Method: api.MethodRemove, Index: &api.IndexArgs{Index: 4}},
		event: queueIs("s0", "s1", "s2", "s3"),
	},
	{call: api.Call{Method: api.MethodClearUpNext}, event: queueIs("s0", "s1")},
	{
		call:  api.Call{Method: api.MethodToggleShuffle},
		event: stateIs(func(st api.State) bool { return st.Shuffled }),
	},
	{
		call:  api.Call{Method: api.MethodPlay, IDs: &api.IDsArgs{IDs: []string{"p1"}, Songs: []catalog.Song{{ID: "p1"}}}},
		event: stateIs(func(st api.State) bool { return st.Current == "p1" }),
	},
	{
		call:  api.Call{Method: api.MethodSearch, Query: &api.QueryArgs{Query: "lofi"}},
		serve: answer("search", "lofi", catalogtest.Reply{}),
		reply: func(r api.Reply) bool { return r.Results != nil },
	},
	{
		call:  api.Call{Method: api.MethodAlbum, Ref: &api.RefArgs{ID: "MPREb_x"}},
		serve: answer("album", "MPREb_x", catalogtest.Reply{}),
		reply: func(r api.Reply) bool { return r.Album != nil },
	},
	{
		call:  api.Call{Method: api.MethodPlaylist, Ref: &api.RefArgs{ID: "PL"}},
		serve: answer("playlist", "PL", catalogtest.Reply{}),
		reply: func(r api.Reply) bool { return r.Playlist != nil },
	},
	{
		call:  api.Call{Method: api.MethodHome},
		serve: answer("home", "", catalogtest.Reply{}),
		reply: func(r api.Reply) bool { return r.Home != nil },
	},
	{
		call:  api.Call{Method: api.MethodExplore},
		serve: answer("explore", "", catalogtest.Reply{}),
		reply: func(r api.Reply) bool { return r.Explore != nil },
	},
	{
		call:  api.Call{Method: api.MethodAccount},
		serve: answer("account", "", catalogtest.Reply{}),
		reply: func(r api.Reply) bool { return r.Account != nil },
	},
	{
		call:  api.Call{Method: api.MethodLibrary},
		serve: answer("library", "", catalogtest.Reply{}),
		reply: func(r api.Reply) bool { return r.Library != nil },
	},
	{
		call:  api.Call{Method: api.MethodLike, Like: &api.LikeArgs{ID: "s0", Liked: true}},
		serve: answer("like", "+s0", catalogtest.Reply{}),
		reply: noError,
	},
	{
		call:  api.Call{Method: api.MethodRefresh, Part: &api.PartArgs{Part: api.PartLibrary}},
		serve: answer("library", "", catalogtest.Reply{}),
		event: func(ev api.Event) bool { return ev.Library != nil },
	},
	{
		call: signIn("SID=1"),
		serve: func(h *harness) {
			answer("signin", "SID=1", catalogtest.Reply{Account: catalog.Account{Name: "Ada"}})(h)
			h.src.Take(h.t, nil, "account", "library", "playlist LM")
		},
		reply: func(r api.Reply) bool { return r.Account != nil && r.Account.Name == "Ada" },
	},
	{call: api.Call{Method: api.MethodSignOut}, serve: answer("signout", "", catalogtest.Reply{}), reply: noError},
	{call: api.Call{Method: api.MethodHandoff, Hand: &api.HandoffArgs{}}, stops: true},
	{call: api.Call{Method: api.MethodShutdown}, stops: true},
}

// Every Method of this Proto is served: it does what it is for, which a
// method left out of dispatch would not.
func TestEveryMethodServed(t *testing.T) {
	served := map[api.Method]bool{}
	for _, step := range methodSteps {
		if step.serve == nil && step.reply == nil && step.event == nil && !step.stops {
			t.Fatalf("%s proves nothing", step.call.Method)
		}
		served[step.call.Method] = true
	}
	for m := api.MethodPing; m.IsValid(); m++ {
		if !served[m] {
			t.Errorf("%s has no step", m)
		}
	}

	h := newHarness(t, Options{Seed: songs(2)})
	c := h.connect(true)
	c.event() // the snapshot
	var at api.Method
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("failed at %s", at)
		}
	})
	for _, step := range methodSteps {
		at = step.call.Method
		if step.stops {
			runStoppingStep(t, step)
			continue
		}
		id := c.send(step.call)
		if step.serve != nil {
			step.serve(h)
		}
		r := c.reply(id)
		if step.reply == nil && r.Err != nil || step.reply != nil && !step.reply(r) {
			t.Fatalf("%s: reply %+v", step.call.Method, r)
		}
		if step.event != nil {
			c.eventWith(step.event)
		}
	}
}

func runStoppingStep(t *testing.T, step methodStep) {
	t.Helper()
	h := newHarness(t, Options{})
	c := h.connect(false)
	c.call(step.call)
	select {
	case <-h.svc.Done():
	case <-time.After(wait):
		t.Fatalf("%s did not stop the Service", step.call.Method)
	}
}

// A method its route does not serve fails rather than answering as done.
func TestUnknownMethod(t *testing.T) {
	s, _ := benchService(t, 3)
	ping := &api.Call{Method: api.MethodPing}
	if err := s.control(ping); !errors.Is(err, errUnknownMethod) {
		t.Errorf("control of a Ping: %v, want errUnknownMethod", err)
	}
	if r := ask(t.Context(), catalogtest.New(t), ping); r.Err == nil || r.Err.Code != api.CodeInternal {
		t.Errorf("ask of a Ping: %+v, want an internal error", r)
	}
}
