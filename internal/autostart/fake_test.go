package autostart

import (
	"errors"
	"io/fs"
)

// commands records the commands it is given instead of running them.
type commands struct {
	argv [][]string
	err  error
}

func (c *commands) run(name string, args ...string) error {
	c.argv = append(c.argv, append([]string{name}, args...))
	return c.err
}

// fakeHive is a registry in memory, keyed by key and value name.
type fakeHive struct {
	values map[string]fakeValue
	writes int
	err    error
}

type fakeValue struct {
	data string
	kind uint32
}

func newFakeHive() *fakeHive { return &fakeHive{values: map[string]fakeValue{}} }

func (h *fakeHive) getString(key, name string) (string, uint32, error) {
	if h.err != nil {
		return "", 0, h.err
	}
	v, ok := h.values[key+`\`+name]
	if !ok {
		return "", 0, fs.ErrNotExist
	}
	return v.data, v.kind, nil
}

func (h *fakeHive) setString(key, name, value string, kind uint32) error {
	h.writes++
	h.values[key+`\`+name] = fakeValue{data: value, kind: kind}
	return nil
}

func (h *fakeHive) deleteValue(key, name string) error {
	h.writes++
	if _, ok := h.values[key+`\`+name]; !ok {
		return fs.ErrNotExist
	}
	delete(h.values, key+`\`+name)
	return nil
}

func (h *fakeHive) path() fakeValue { return h.values[envKeyPath+`\`+pathValue] }

var errCommand = errors.New("command failed")
