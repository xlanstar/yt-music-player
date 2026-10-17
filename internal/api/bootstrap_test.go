package api

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// bootstrapFrames are the messages a hand-off needs, as testdata's stream
// holds them.
func bootstrapFrames() []Frame {
	return []Frame{
		{Hello: &Hello{Proto: 1, Version: "1.2.3", Role: RoleDesktop, Subscribe: true, PID: 4242}},
		{ID: 1, Call: &Call{Method: MethodPing}},
		{ID: 2, Call: &Call{Method: MethodHandoff, Hand: &HandoffArgs{Wait: 5 * time.Second}}},
		{Event: &Event{Handoff: &HandoffEvent{By: 4242, Wait: 5 * time.Second}}},
		{ID: 2, Reply: &Reply{Err: &Error{Code: CodeInternal, Detail: "detail"}}},
		{ID: 3, Call: &Call{Method: MethodShutdown}},
	}
}

// The bootstrap of Proto 1 as an old peer reads it: only what never
// changes, in plain types, so renaming an api type changes nothing here.
type (
	oldFrame struct {
		ID    uint32
		Hello *oldHello
		Call  *oldCall
		Reply *oldReply
		Event *oldEvent
	}
	oldHello struct {
		Proto     uint16
		Version   string
		Role      uint8
		Subscribe bool
		PID       int
	}
	oldCall struct {
		Method uint16
		Hand   *oldHandoffArgs
	}
	oldHandoffArgs struct{ Wait int64 }
	oldReply       struct{ Err *oldError }
	oldError       struct {
		Code   uint16
		Detail string
	}
	oldEvent        struct{ Handoff *oldHandoffEvent }
	oldHandoffEvent struct {
		By   int
		Wait int64
	}
)

// oldFrames are bootstrapFrames as an old peer reads them.
func oldFrames() []oldFrame {
	wait := int64(5 * time.Second)
	return []oldFrame{
		{Hello: &oldHello{Proto: 1, Version: "1.2.3", Role: 1, Subscribe: true, PID: 4242}},
		{ID: 1, Call: &oldCall{Method: 1}},
		{ID: 2, Call: &oldCall{Method: 2, Hand: &oldHandoffArgs{Wait: wait}}},
		{Event: &oldEvent{Handoff: &oldHandoffEvent{By: 4242, Wait: wait}}},
		{ID: 2, Reply: &oldReply{Err: &oldError{Code: 4, Detail: "detail"}}},
		{ID: 3, Call: &oldCall{Method: 3}},
	}
}

// TestBootstrapFrozen holds that peers of any Proto still hand off.
// testdata/bootstrap-proto1.hex, never changed, is what a Proto 1 peer sent
// of bootstrapFrames, the types of its whole Frame first: today's Frame
// must read it (which fails too when any field of Frame changes type), and
// an old peer must read today's stream alike.
func TestBootstrapFrozen(t *testing.T) {
	text, err := os.ReadFile("testdata/bootstrap-proto1.hex")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := hex.DecodeString(strings.Join(strings.Fields(string(text)), ""))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("old-to-new", func(t *testing.T) {
		dec := gob.NewDecoder(bytes.NewReader(stream))
		for i, want := range bootstrapFrames() {
			var got Frame
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("frame %d:\n got %+v\nwant %+v", i, got, want)
			}
		}
	})

	t.Run("new-to-old", func(t *testing.T) {
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		for _, f := range bootstrapFrames() {
			if err := enc.Encode(&f); err != nil {
				t.Fatal(err)
			}
		}
		for name, r := range map[string]*bytes.Reader{"proto 1": bytes.NewReader(stream), "today": bytes.NewReader(buf.Bytes())} {
			dec := gob.NewDecoder(r)
			for i, want := range oldFrames() {
				var got oldFrame
				if err := dec.Decode(&got); err != nil {
					t.Fatalf("%s, frame %d: %v", name, i, err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s, frame %d:\n got %+v\nwant %+v", name, i, got, want)
				}
			}
		}
	})
}
