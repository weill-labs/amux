package render

import "github.com/weill-labs/amux/internal/config"

// IconSet centralizes human-facing renderer glyphs. Structured capture and
// event APIs remain semantic and should not consume this type.
type IconSet struct {
	PaneIdle           string
	PaneActive         string
	PaneBusy           string
	PaneLead           string
	PaneEscalated      string
	PaneStuck          string
	PaneNameOpen       string
	PaneNameClose      string
	RemoteHost         string
	RemoteConnected    string
	RemoteReconnecting string
	RemoteDisconnected string
	PR                 string
	Issue              string
	Task               string
	CopyMode           string
	ToggleOn           string // modal title-bar radio, selected
	ToggleOff          string // modal title-bar radio, unselected
}

// IconSetPreset pairs a validated config name with its renderer glyphs.
type IconSetPreset struct {
	Name string
	Set  IconSet
}

// DefaultIconSet returns the renderer's backward-compatible icon set.
func DefaultIconSet() IconSet {
	return UnicodeIconSet()
}

// IconSetPresets returns the renderer's named icon presets in config order.
func IconSetPresets() []IconSetPreset {
	return []IconSetPreset{
		{Name: config.ThemeIconsASCII, Set: ASCIIIconSet()},
		{Name: config.ThemeIconsUnicode, Set: UnicodeIconSet()},
		{Name: config.ThemeIconsNerd, Set: NerdFontIconSet()},
	}
}

// IconSetForName returns the preset icon set for a validated config name.
func IconSetForName(name string) (IconSet, bool) {
	for _, preset := range IconSetPresets() {
		if preset.Name == name {
			return preset.Set, true
		}
	}
	return IconSet{}, false
}

// UnicodeIconSet returns the current default compact Unicode glyphs.
func UnicodeIconSet() IconSet {
	return IconSet{
		PaneIdle:           "◇",
		PaneActive:         "●",
		PaneBusy:           "○",
		PaneLead:           "▶",
		PaneEscalated:      "◆",
		PaneStuck:          "◈",
		PaneNameOpen:       "[",
		PaneNameClose:      "]",
		RemoteHost:         "@",
		RemoteConnected:    "●",
		RemoteReconnecting: "⚠",
		RemoteDisconnected: "✗",
		PR:                 "#",
		Issue:              "",
		Task:               "",
		CopyMode:           "[copy]",
		ToggleOn:           "◉",
		ToggleOff:          "○",
	}
}

// ASCIIIconSet returns single-cell fallbacks for terminals with limited glyph support.
func ASCIIIconSet() IconSet {
	return IconSet{
		PaneIdle:           ".",
		PaneActive:         "*",
		PaneBusy:           "o",
		PaneLead:           ">",
		PaneEscalated:      "!",
		PaneStuck:          "x",
		PaneNameOpen:       "[",
		PaneNameClose:      "]",
		RemoteHost:         "@",
		RemoteConnected:    "*",
		RemoteReconnecting: "!",
		RemoteDisconnected: "x",
		PR:                 "#",
		Issue:              "I",
		Task:               "T",
		CopyMode:           "C",
		ToggleOn:           "(*)",
		ToggleOff:          "( )",
	}
}

// NerdFontIconSet returns Private Use Area glyphs for opt-in Nerd Font rendering.
func NerdFontIconSet() IconSet {
	return IconSet{
		PaneIdle:           "\uebb5",
		PaneActive:         "\uebb4",
		PaneBusy:           "\ueb31",
		PaneLead:           "\ueb59",
		PaneEscalated:      "\uea6c",
		PaneStuck:          "\ueaaf",
		PaneNameOpen:       "[",
		PaneNameClose:      "]",
		RemoteHost:         "\ueb50",
		RemoteConnected:    "\uebb4",
		RemoteReconnecting: "\uea6c",
		RemoteDisconnected: "\ueaaf",
		PR:                 "\uf407",
		Issue:              "\uf41b",
		Task:               "\ueb67",
		CopyMode:           "\ueac0",
		ToggleOn:           "\uf192", // nf-fa-dot_circle_o
		ToggleOff:          "\uf10c", // nf-fa-circle_o
	}
}

func normalizeIconSet(icons IconSet) IconSet {
	if icons == (IconSet{}) {
		return DefaultIconSet()
	}
	return icons
}
