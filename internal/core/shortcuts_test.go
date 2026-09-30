package core

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseShortcuts(t *testing.T) {
	data := []byte(`[
		{"modmask":64,"key":"W","description":"Close window","has_description":true,"dispatcher":"__lua","arg":"68","submap":""},
		{"modmask":64,"key":"Q","description":"Close window","has_description":true,"dispatcher":"__lua","arg":"70","submap":""},
		{"modmask":65,"key":"RETURN","description":"Browser","has_description":true,"dispatcher":"__lua","arg":"317","submap":""},
		{"modmask":64,"key":"","keycode":0,"description":"Switch to workspace 3","has_description":true,"dispatcher":"__lua","arg":"114","submap":""},
		{"modmask":13,"key":"comma","description":"All modifiers","has_description":true,"dispatcher":"__lua","arg":"9","submap":""},
		{"modmask":64,"key":"mouse:272","description":"Move window","has_description":true,"mouse":true,"dispatcher":"__lua","arg":"200","submap":""},
		{"modmask":64,"key":"R","description":"Resize mode","has_description":true,"dispatcher":"__lua","arg":"201","submap":"resize"},
		{"modmask":64,"key":"E","description":"","has_description":false,"dispatcher":"__lua","arg":"202","submap":""},
		{"modmask":64,"key":"X","description":"Old style","has_description":true,"dispatcher":"exec","arg":"kitty","submap":""},
		{"modmask":64,"key":"Y","description":"Bad reference","has_description":true,"dispatcher":"__lua","arg":"1); os.exit(","submap":""}
	]`)
	got, err := parseShortcuts(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Shortcut{
		{Ref: "68", Keys: "SUPER W", Description: "Close window"},
		{Ref: "317", Keys: "SUPER SHIFT RETURN", Description: "Browser"},
		{Ref: "114", Keys: "SUPER", Description: "Switch to workspace 3"},
		{Ref: "9", Keys: "CTRL ALT SHIFT comma", Description: "All modifiers"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shortcuts:\n got %+v\nwant %+v", got, want)
	}
	if _, err := parseShortcuts([]byte("not json")); err == nil {
		t.Fatal("no error for bad JSON")
	}
}

func TestParseWorkspaces(t *testing.T) {
	got, err := parseWorkspaces([]byte(`[{"id":-98,"windows":1},{"id":2,"windows":3},{"id":10,"windows":0}]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Workspace{{2, 3}, {10, 0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workspaces %+v, want %+v", got, want)
	}
}

func TestActionLua(t *testing.T) {
	cases := []struct {
		body shortcutBody
		want string
	}{
		{shortcutBody{Action: "workspace", Workspace: 3}, `hl.dsp.focus({ workspace = "3" })`},
		{shortcutBody{Action: "moveToWorkspace", Workspace: 10}, `hl.dsp.window.move({ workspace = "10" })`},
		{shortcutBody{Action: "focus", Direction: "l"}, `hl.dsp.focus({ direction = "l" })`},
		{shortcutBody{Action: "swap", Direction: "d"}, `hl.dsp.window.swap({ direction = "d" })`},
		{shortcutBody{Action: "close"}, `hl.dsp.window.close()`},
		{shortcutBody{Action: "scratchpad"}, `hl.dsp.workspace.toggle_special("scratchpad")`},
	}
	for _, c := range cases {
		got, err := actionLua(c.body)
		if err != nil || got != c.want {
			t.Errorf("%+v: got %q, %v, want %q", c.body, got, err, c.want)
		}
	}
	for _, bad := range []shortcutBody{
		{Action: "workspace", Workspace: 0},
		{Action: "workspace", Workspace: 11},
		{Action: "focus", Direction: "x"},
		{Action: "swap", Direction: `l" }) os.exit() --`},
		{Action: "exec"},
	} {
		if lua, err := actionLua(bad); err == nil {
			t.Errorf("%+v: no error, Lua %q", bad, lua)
		}
	}
}

func TestRunShortcutRefusesAnInvalidReference(t *testing.T) {
	d, _ := inputDaemon(t, true)
	for _, ref := range []string{"abc", "1)", "12345678901", "-1"} {
		err := d.runShortcut(shortcutBody{Run: ref})
		if err == nil || !strings.Contains(err.Error(), "not valid") {
			t.Errorf("run %q: %v", ref, err)
		}
	}
}

func TestNiriAction(t *testing.T) {
	cases := []struct {
		b    shortcutBody
		want string
	}{
		{shortcutBody{Action: "close"}, "close-window"},
		{shortcutBody{Action: "workspace", Workspace: 3}, "focus-workspace 3"},
		{shortcutBody{Action: "moveToWorkspace", Workspace: 10}, "move-window-to-workspace 10"},
		{shortcutBody{Action: "focus", Direction: "u"}, "focus-window-up"},
		{shortcutBody{Action: "swap", Direction: "l"}, "move-column-left"},
	}
	for _, c := range cases {
		got, err := niriAction(c.b)
		if err != nil || strings.Join(got, " ") != c.want {
			t.Errorf("%+v: got %v, %v, want %q", c.b, got, err, c.want)
		}
	}
	for _, b := range []shortcutBody{
		{Action: "workspace", Workspace: 0},
		{Action: "workspace", Workspace: 11},
		{Action: "focus", Direction: "x"},
		{Action: "scratchpad"},
		{Action: "rm -rf"},
	} {
		if _, err := niriAction(b); err == nil {
			t.Errorf("%+v: want an error", b)
		}
	}
}

func TestParseNiriWorkspaces(t *testing.T) {
	ws := `[{"id":5,"idx":2,"output":"eDP-1","is_focused":true},
		{"id":4,"idx":1,"output":"eDP-1","is_focused":false},
		{"id":9,"idx":1,"output":"HDMI-A-1","is_focused":false}]`
	wins := `[{"workspace_id":5},{"workspace_id":5},{"workspace_id":9},{"workspace_id":null}]`
	got, active, err := parseNiriWorkspaces([]byte(ws), []byte(wins))
	if err != nil {
		t.Fatal(err)
	}
	want := []Workspace{{ID: 1, Windows: 0}, {ID: 2, Windows: 2}}
	if active != 2 || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, active %d, want %v, active 2", got, active, want)
	}
}
