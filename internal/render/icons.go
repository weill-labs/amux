package render

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
	CopyMode      string
	Connected     string
	Reconnecting  string
	Disconnected  string
}

// DefaultIconSet returns the renderer's backward-compatible icon set.
func DefaultIconSet() IconSet {
	return UnicodeIconSet()
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
		CopyMode:      "C",
		Connected:     "+",
		Reconnecting:  "~",
		Disconnected:  "x",
	}
}

// NerdFontIconSet returns placeholder Private Use Area glyphs for future opt-in use.
func NerdFontIconSet() IconSet {
	return IconSet{
		PaneIdle:      "\uf10c",
		PaneActive:    "\uf111",
		PaneBusy:      "\uf013",
		PaneLead:      "\uf04b",
		PaneEscalated: "\uf071",
		PaneStuck:     "\uf188",
		RemoteHost:    "\uf489",
		PR:            "\uf407",
		Issue:         "\uf41b",
		CopyMode:      "\uf0c5",
		Connected:     "\uf0e7",
		Reconnecting:  "\uf021",
		Disconnected:  "\uf00d",
	}
}

func normalizeIconSet(icons IconSet) IconSet {
	if icons == (IconSet{}) {
		return DefaultIconSet()
	}
	return icons
}
