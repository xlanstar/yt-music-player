package audio

import (
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/rs/zerolog/log"
)

// Linux desktops show what plays, and send media keys, through MPRIS on
// the session bus (https://specifications.freedesktop.org/mpris-spec/).
// The engine's controls export its two interfaces; properties are served
// by hand, as Position is read when asked.

const (
	mprisName   = "org.mpris.MediaPlayer2.ytmusicplayer"
	mprisPath   = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	mprisRoot   = "org.mpris.MediaPlayer2"
	mprisPlayer = "org.mpris.MediaPlayer2.Player"
	propsIface  = "org.freedesktop.DBus.Properties"
	// seekedSlack: a position this far from where the clock would have
	// moved it was a seek, which MPRIS signals.
	seekedSlack = time.Second
)

type mpris struct {
	conn    *dbus.Conn
	command func(Event)

	mu sync.Mutex
	np nowPlaying
	// at is when np was published, from which Position extrapolates.
	at    time.Time
	track int
}

func newControls(command func(Event)) controls {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Debug().Err(err).Msg("no session bus for MPRIS")
		return noControls{}
	}
	m := &mpris{conn: conn, command: command, at: time.Now()}
	if err := m.export(); err != nil {
		log.Debug().Err(err).Msg("MPRIS export failed")
		_ = conn.Close()
		return noControls{}
	}
	return m
}

func (m *mpris) export() error {
	// Go names for methods whose MPRIS names Go reserves or spells
	// otherwise.
	names := map[string]string{"SeekBy": "Seek", "OpenURI": "OpenUri"}
	for _, e := range []struct {
		v     any
		iface string
	}{
		{mprisRootMethods{}, mprisRoot},
		{mprisPlayerMethods{m}, mprisPlayer},
		{mprisProps{m}, propsIface},
		{introspectable(mprisIntrospection), "org.freedesktop.DBus.Introspectable"},
	} {
		if err := m.conn.ExportWithMap(e.v, names, mprisPath, e.iface); err != nil {
			return err
		}
	}
	// A second instance takes a name of its own, as the specification asks.
	name := mprisName
	reply, err := m.conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err == nil && reply != dbus.RequestNameReplyPrimaryOwner {
		name += ".instance" + strconv.Itoa(os.Getpid())
		_, err = m.conn.RequestName(name, dbus.NameFlagDoNotQueue)
	}
	return err
}

func (m *mpris) claim() {}

func (m *mpris) publish(np nowPlaying) {
	now := time.Now()
	m.mu.Lock()
	expected := m.positionLocked(now)
	newTrack := np.Info != m.np.Info
	if newTrack {
		m.track++
	}
	m.np, m.at = np, now
	changed := map[string]dbus.Variant{
		"PlaybackStatus": dbus.MakeVariant(m.statusLocked()),
		"Metadata":       dbus.MakeVariant(m.metadataLocked()),
		"CanPlay":        dbus.MakeVariant(np.state != stateStopped),
		"CanPause":       dbus.MakeVariant(np.state != stateStopped),
		"CanSeek":        dbus.MakeVariant(np.duration > 0),
	}
	seeked := !newTrack && np.state != stateStopped && math.Abs(np.elapsed-expected) > seekedSlack.Seconds()
	m.mu.Unlock()
	_ = m.conn.Emit(mprisPath, propsIface+".PropertiesChanged", mprisPlayer, changed, []string{})
	if seeked {
		_ = m.conn.Emit(mprisPath, mprisPlayer+".Seeked", micros(np.elapsed))
	}
}

func (m *mpris) close() { _ = m.conn.Close() }

// positionLocked is where the song is now, in seconds; the caller holds mu.
func (m *mpris) positionLocked(now time.Time) float64 {
	pos := m.np.elapsed
	if m.np.advancing {
		pos += now.Sub(m.at).Seconds()
	}
	if m.np.duration > 0 {
		pos = min(pos, m.np.duration)
	}
	return pos
}

func (m *mpris) statusLocked() string {
	switch m.np.state {
	case statePlaying:
		return "Playing"
	case statePaused:
		return "Paused"
	}
	return "Stopped"
}

func (m *mpris) trackIDLocked() dbus.ObjectPath {
	return dbus.ObjectPath("/dev/lanstar/ytmusicplayer/track/" + strconv.Itoa(m.track))
}

func (m *mpris) metadataLocked() map[string]dbus.Variant {
	if m.np.state == stateStopped {
		return map[string]dbus.Variant{"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack"))}
	}
	md := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(m.trackIDLocked()),
		"xesam:title":   dbus.MakeVariant(m.np.Title),
		"xesam:artist":  dbus.MakeVariant([]string{m.np.Artist}),
	}
	if m.np.Album != "" {
		md["xesam:album"] = dbus.MakeVariant(m.np.Album)
	}
	if m.np.duration > 0 {
		md["mpris:length"] = dbus.MakeVariant(micros(m.np.duration))
	}
	return md
}

func micros(sec float64) int64 { return int64(sec * 1e6) }

// props returns an interface's properties.
func (m *mpris) props(iface string) (map[string]dbus.Variant, bool) {
	switch iface {
	case mprisRoot:
		return map[string]dbus.Variant{
			"CanQuit":             dbus.MakeVariant(false),
			"CanRaise":            dbus.MakeVariant(false),
			"HasTrackList":        dbus.MakeVariant(false),
			"Identity":            dbus.MakeVariant("YT Music Player"),
			"DesktopEntry":        dbus.MakeVariant("yt-music-player"),
			"SupportedUriSchemes": dbus.MakeVariant([]string{}),
			"SupportedMimeTypes":  dbus.MakeVariant([]string{}),
		}, true
	case mprisPlayer:
		m.mu.Lock()
		defer m.mu.Unlock()
		active := m.np.state != stateStopped
		return map[string]dbus.Variant{
			"PlaybackStatus": dbus.MakeVariant(m.statusLocked()),
			"Rate":           dbus.MakeVariant(1.0),
			"MinimumRate":    dbus.MakeVariant(1.0),
			"MaximumRate":    dbus.MakeVariant(1.0),
			"Metadata":       dbus.MakeVariant(m.metadataLocked()),
			"Volume":         dbus.MakeVariant(1.0),
			"Position":       dbus.MakeVariant(micros(m.positionLocked(time.Now()))),
			"CanGoNext":      dbus.MakeVariant(true),
			"CanGoPrevious":  dbus.MakeVariant(true),
			"CanPlay":        dbus.MakeVariant(active),
			"CanPause":       dbus.MakeVariant(active),
			"CanSeek":        dbus.MakeVariant(m.np.duration > 0),
			"CanControl":     dbus.MakeVariant(true),
		}, true
	}
	return nil, false
}

type mprisProps struct{ m *mpris }

func (p mprisProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	all, ok := p.m.props(iface)
	if !ok {
		return dbus.Variant{}, dbus.MakeFailedError(errUnknownInterface)
	}
	v, ok := all[name]
	if !ok {
		return dbus.Variant{}, dbus.MakeFailedError(errUnknownProperty)
	}
	return v, nil
}

func (p mprisProps) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	all, ok := p.m.props(iface)
	if !ok {
		return nil, dbus.MakeFailedError(errUnknownInterface)
	}
	return all, nil
}

// Set refuses every property: volume is the app's own.
func (mprisProps) Set(string, string, dbus.Variant) *dbus.Error {
	return dbus.MakeFailedError(errReadOnly)
}

type mprisError string

func (e mprisError) Error() string { return string(e) }

const (
	errUnknownInterface = mprisError("audio: no such interface")
	errUnknownProperty  = mprisError("audio: no such property")
	errReadOnly         = mprisError("audio: read-only property")
	errNoURIs           = mprisError("audio: opens no URIs")
)

type mprisRootMethods struct{}

func (mprisRootMethods) Raise() *dbus.Error { return nil }
func (mprisRootMethods) Quit() *dbus.Error  { return nil }

type mprisPlayerMethods struct{ m *mpris }

func (p mprisPlayerMethods) send(k Kind, at float64) *dbus.Error {
	p.m.command(Event{Kind: k, At: at})
	return nil
}

func (p mprisPlayerMethods) Next() *dbus.Error      { return p.send(CommandNext, 0) }
func (p mprisPlayerMethods) Previous() *dbus.Error  { return p.send(CommandPrev, 0) }
func (p mprisPlayerMethods) Pause() *dbus.Error     { return p.send(CommandPause, 0) }
func (p mprisPlayerMethods) PlayPause() *dbus.Error { return p.send(CommandToggle, 0) }
func (p mprisPlayerMethods) Stop() *dbus.Error      { return p.send(CommandPause, 0) }
func (p mprisPlayerMethods) Play() *dbus.Error      { return p.send(CommandPlay, 0) }

// SeekBy is MPRIS's Seek: it moves by offset microseconds, no further
// than the end, which then ends the song as MPRIS's Next would.
func (p mprisPlayerMethods) SeekBy(offset int64) *dbus.Error {
	p.m.mu.Lock()
	at := p.m.positionLocked(time.Now()) + float64(offset)/1e6
	if d := p.m.np.duration; d > 0 {
		at = min(at, d)
	}
	p.m.mu.Unlock()
	return p.send(CommandSeek, max(at, 0))
}

// SetPosition moves to pos microseconds, if track still plays; MPRIS
// has a position before the start or past the length do nothing.
func (p mprisPlayerMethods) SetPosition(track dbus.ObjectPath, pos int64) *dbus.Error {
	p.m.mu.Lock()
	isCurrent := track == p.m.trackIDLocked()
	isPastEnd := p.m.np.duration > 0 && pos > micros(p.m.np.duration)
	p.m.mu.Unlock()
	if !isCurrent || pos < 0 || isPastEnd {
		return nil
	}
	return p.send(CommandSeek, float64(pos)/1e6)
}

func (mprisPlayerMethods) OpenURI(string) *dbus.Error {
	return dbus.MakeFailedError(errNoURIs)
}

type introspectable string

func (i introspectable) Introspect() (string, *dbus.Error) { return string(i), nil }

const mprisIntrospection = `<node>
 <interface name="org.mpris.MediaPlayer2">
  <method name="Raise"/>
  <method name="Quit"/>
  <property name="CanQuit" type="b" access="read"/>
  <property name="CanRaise" type="b" access="read"/>
  <property name="HasTrackList" type="b" access="read"/>
  <property name="Identity" type="s" access="read"/>
  <property name="DesktopEntry" type="s" access="read"/>
  <property name="SupportedUriSchemes" type="as" access="read"/>
  <property name="SupportedMimeTypes" type="as" access="read"/>
 </interface>
 <interface name="org.mpris.MediaPlayer2.Player">
  <method name="Next"/>
  <method name="Previous"/>
  <method name="Pause"/>
  <method name="PlayPause"/>
  <method name="Stop"/>
  <method name="Play"/>
  <method name="Seek"><arg name="Offset" type="x" direction="in"/></method>
  <method name="SetPosition"><arg name="TrackId" type="o" direction="in"/><arg name="Position" type="x" direction="in"/></method>
  <method name="OpenUri"><arg name="Uri" type="s" direction="in"/></method>
  <signal name="Seeked"><arg name="Position" type="x"/></signal>
  <property name="PlaybackStatus" type="s" access="read"/>
  <property name="Rate" type="d" access="read"/>
  <property name="Metadata" type="a{sv}" access="read"/>
  <property name="Volume" type="d" access="read"/>
  <property name="Position" type="x" access="read"/>
  <property name="MinimumRate" type="d" access="read"/>
  <property name="MaximumRate" type="d" access="read"/>
  <property name="CanGoNext" type="b" access="read"/>
  <property name="CanGoPrevious" type="b" access="read"/>
  <property name="CanPlay" type="b" access="read"/>
  <property name="CanPause" type="b" access="read"/>
  <property name="CanSeek" type="b" access="read"/>
  <property name="CanControl" type="b" access="read"/>
 </interface>
 <interface name="org.freedesktop.DBus.Properties">
  <method name="Get"><arg name="interface" type="s" direction="in"/><arg name="property" type="s" direction="in"/><arg name="value" type="v" direction="out"/></method>
  <method name="GetAll"><arg name="interface" type="s" direction="in"/><arg name="properties" type="a{sv}" direction="out"/></method>
  <method name="Set"><arg name="interface" type="s" direction="in"/><arg name="property" type="s" direction="in"/><arg name="value" type="v" direction="in"/></method>
  <signal name="PropertiesChanged"><arg name="interface" type="s"/><arg name="changed" type="a{sv}"/><arg name="invalidated" type="as"/></signal>
 </interface>
</node>`
