package render

import "github.com/weill-labs/amux/internal/config"

// IconSet centralizes human-facing renderer glyphs. Structured capture and
// event APIs remain semantic and should not consume this type.
type IconSet struct {
	PaneIdle      string
	PaneActive    string
	PaneBusy      string
	PaneLead      string
	PaneEscalated string
	PaneStuck     string
	RemoteHost    string
	PR            string
	Issue         string
	Task          string
	CopyMode      string
	Connected     string
	Reconnecting  string
	Disconnected  string
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
		PaneIdle:      "◇",
		PaneActive:    "●",
		PaneBusy:      "○",
		PaneLead:      "▶",
		PaneEscalated: "◆",
		PaneStuck:     "◈",
		RemoteHost:    "@",
		PR:            "#",
		Issue:         "",
		Task:          "",
		CopyMode:      "[copy]",
		Connected:     "⚡",
		Reconnecting:  "⟳",
		Disconnected:  "✕",
	}
}

// ASCIIIconSet returns single-cell fallbacks for terminals with limited glyph support.
func ASCIIIconSet() IconSet {
	return IconSet{
		PaneIdle:      ".",
		PaneActive:    "*",
		PaneBusy:      "o",
		PaneLead:      ">",
		PaneEscalated: "!",
		PaneStuck:     "x",
		RemoteHost:    "@",
		PR:            "#",
		Issue:         "I",
		Task:          "T",
		CopyMode:      "C",
		Connected:     "+",
		Reconnecting:  "~",
		Disconnected:  "x",
	}
}

// NerdFontIconSet returns Private Use Area glyphs for opt-in Nerd Font rendering.
func NerdFontIconSet() IconSet {
	return IconSet{
		PaneIdle:      "\uebb5",
		PaneActive:    "\uebb4",
		PaneBusy:      "\ueb31",
		PaneLead:      "\ueb59",
		PaneEscalated: "\uea6c",
		PaneStuck:     "\ueaaf",
		RemoteHost:    "\ueb50",
		PR:            "\uf407",
		Issue:         "\uf41b",
		Task:          "\ueb67",
		CopyMode:      "\ueac0",
		Connected:     "\U000f0c53",
		Reconnecting:  "\uea77",
		Disconnected:  "\U000f0c9b",
	}
}

func normalizeIconSet(icons IconSet) IconSet {
	if icons == (IconSet{}) {
		return DefaultIconSet()
	}
	return icons
}
