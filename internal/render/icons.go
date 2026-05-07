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
	PaneNameOpen  string
	PaneNameClose string
	RemoteHost    string
	PR            string
	Issue         string
	Task          string
	CopyMode      string
	Connected     string
	Reconnecting  string
	Disconnected  string
}

// DefaultIconSet returns the renderer's backward-compatible icon set.
func DefaultIconSet() IconSet {
	return UnicodeIconSet()
}

// IconSetForName returns the preset icon set for a validated config name.
func IconSetForName(name string) (IconSet, bool) {
	switch name {
	case config.ThemeIconsASCII:
		return ASCIIIconSet(), true
	case config.ThemeIconsUnicode:
		return UnicodeIconSet(), true
	case config.ThemeIconsNerd:
		return NerdFontIconSet(), true
	default:
		return IconSet{}, false
	}
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
		PaneNameOpen:  "[",
		PaneNameClose: "]",
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
		PaneNameOpen:  "[",
		PaneNameClose: "]",
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
		PaneNameOpen:  "[",
		PaneNameClose: "]",
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
