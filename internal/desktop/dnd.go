package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// makoMode is the mako mode that Omarchy and the mako docs use for Do Not
// Disturb.
const makoMode = "do-not-disturb"

// DND reads and sets Do Not Disturb of the desktop notification service.
// It supports the notification service of the Omarchy shell, the iNiR
// shell, and mako.
type DND struct {
	kind      string
	statePath string
	inirPath  string

	// The state file of the Omarchy shell changes only when Do Not Disturb
	// changes. Get reads the file again only when its time or size changes.
	mu       sync.Mutex
	cached   bool
	modTime  time.Time
	size     int64
	cachedOn bool
}

// NewDND picks the notification service of this desktop. Kind returns an
// empty string when Flux supports none of the services on this desktop.
func NewDND() *DND {
	home, _ := os.UserHomeDir()
	d := &DND{
		statePath: filepath.Join(home, ".local", "state", "omarchy", "notifications.json"),
		inirPath:  filepath.Join(home, ".config", "quickshell", "inir"),
	}
	switch {
	case onPath("omarchy-shell"):
		d.kind = "omarchy-shell"
	case onPath("qs") && fileExists(filepath.Join(d.inirPath, "shell.qml")):
		// iNiR keeps Do Not Disturb as notifications.silent in its config.
		d.kind = "inir"
		d.statePath = filepath.Join(home, ".config", "inir", "config.json")
	case onPath("makoctl"):
		d.kind = "mako"
	}
	return d
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Kind returns "omarchy-shell", "inir", "mako", or an empty string.
func (d *DND) Kind() string { return d.kind }

// Get returns the Do Not Disturb state. ok is false when the service does
// not answer.
func (d *DND) Get() (on, ok bool) {
	switch d.kind {
	case "omarchy-shell":
		return d.getState(parseOmarchyDND)
	case "inir":
		return d.getState(parseInirDND)
	case "mako":
		out, err := exec.Command("makoctl", "mode").Output()
		if err != nil {
			return false, false
		}
		return makoDND(string(out)), true
	}
	return false, false
}

// getState reads the state file of the shell with parse. The Omarchy shell
// writes its file atomically 200 ms after a change, and iNiR writes its
// config about 300 ms after a change.
func (d *DND) getState(parse func([]byte) (bool, error)) (on, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, err := os.Stat(d.statePath)
	if errors.Is(err, os.ErrNotExist) {
		// The shell writes the file at the first change. With no file,
		// Do Not Disturb is off.
		return false, true
	}
	if err != nil {
		return false, false
	}
	if d.cached && st.ModTime().Equal(d.modTime) && st.Size() == d.size {
		return d.cachedOn, true
	}
	b, err := os.ReadFile(d.statePath)
	if err != nil {
		return false, false
	}
	on, err = parse(b)
	if err != nil {
		return false, false
	}
	d.cached, d.modTime, d.size, d.cachedOn = true, st.ModTime(), st.Size(), on
	return on, true
}

// Set turns Do Not Disturb on or off.
func (d *DND) Set(on bool) error {
	switch d.kind {
	case "omarchy-shell":
		value := "off"
		if on {
			value = "on"
		}
		if out, err := exec.Command("omarchy-shell", "notifications", "setDnd", value).CombinedOutput(); err != nil {
			return fmt.Errorf("omarchy-shell: %w: %s", err, strings.TrimSpace(string(out)))
		}
		// The bar shows the state. omarchy-toggle-notification-silencing
		// refreshes it the same way.
		_ = exec.Command("omarchy-shell", "-q", "omarchy.indicators", "refresh").Run()
		return nil
	case "inir":
		// iNiR offers only a toggle over IPC, so toggle when the state
		// differs.
		if cur, ok := d.Get(); ok && cur == on {
			return nil
		}
		if out, err := exec.Command("qs", "-p", d.inirPath, "ipc", "call", "notifications", "toggleSilent").CombinedOutput(); err != nil {
			return fmt.Errorf("qs: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "mako":
		flag := "-r"
		if on {
			flag = "-a"
		}
		if out, err := exec.Command("makoctl", "mode", flag, makoMode).CombinedOutput(); err != nil {
			return fmt.Errorf("makoctl: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return errors.New("no supported notification service")
}

// parseOmarchyDND reads the "dnd" key of notifications.json. A file with
// no key means off.
func parseOmarchyDND(b []byte) (bool, error) {
	var s struct {
		DND *bool `json:"dnd"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, err
	}
	return s.DND != nil && *s.DND, nil
}

// parseInirDND reads notifications.silent of the iNiR config. A config
// with no key means off.
func parseInirDND(b []byte) (bool, error) {
	var s struct {
		Notifications struct {
			Silent bool `json:"silent"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, err
	}
	return s.Notifications.Silent, nil
}

// makoDND reports whether the output of `makoctl mode` has the Do Not
// Disturb mode.
func makoDND(modes string) bool {
	for _, m := range strings.Fields(modes) {
		if m == makoMode {
			return true
		}
	}
	return false
}
