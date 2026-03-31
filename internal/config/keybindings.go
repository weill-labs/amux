package config

// Binding represents a single key binding: an action name and its arguments.
type Binding struct {
	Action string
	Args   []string
}

// Keybindings holds the resolved dispatch table for client-side input handling.
// The Prefix byte triggers prefix mode; after prefix, the Bindings map
// dispatches the next byte to an action.
type Keybindings struct {
	Prefix   byte
	Bindings map[byte]Binding
}

// DefaultKeybindings returns the built-in default keybindings.
func DefaultKeybindings() *Keybindings {
	return &Keybindings{
		Prefix: 0x01, // Ctrl-a
		Bindings: map[byte]Binding{
			'\\': {Action: "split", Args: []string{"root", "v", "--focus"}},
			'-':  {Action: "split", Args: []string{"--focus"}},
			'|':  {Action: "split", Args: []string{"v", "--focus"}},
			'_':  {Action: "split", Args: []string{"root", "--focus"}},
			'}':  {Action: "swap", Args: []string{"forward"}},
			'{':  {Action: "swap", Args: []string{"backward"}},
			'o':  {Action: "focus", Args: []string{"next"}},
			'h':  {Action: "focus", Args: []string{"left"}},
			'l':  {Action: "focus", Args: []string{"right"}},
			'k':  {Action: "focus", Args: []string{"up"}},
			'j':  {Action: "focus", Args: []string{"down"}},
			'H':  {Action: "resize-active", Args: []string{"left", "2"}},
			'J':  {Action: "resize-active", Args: []string{"down", "2"}},
			'K':  {Action: "resize-active", Args: []string{"up", "2"}},
			'L':  {Action: "resize-active", Args: []string{"right", "2"}},
			'=':  {Action: "equalize"},
			'x':  {Action: "kill"},
			'z':  {Action: "zoom"},
			// Reserve lowercase m so it does not fall through as literal input.
			'm': {Action: "compat-bell"},
			'U': {Action: "undo"},
			'[': {Action: "copy-mode"},
			'c': {Action: "new-window"},
			'a': {Action: "add-pane"},
			'n': {Action: "next-window"},
			'p': {Action: "prev-window"},
			',': {Action: "rename-window"},
			'q': {Action: "display-panes"},
			's': {Action: "choose-tree"},
			'w': {Action: "choose-window"},
			'1': {Action: "select-window", Args: []string{"1"}},
			'2': {Action: "select-window", Args: []string{"2"}},
			'3': {Action: "select-window", Args: []string{"3"}},
			'4': {Action: "select-window", Args: []string{"4"}},
			'5': {Action: "select-window", Args: []string{"5"}},
			'6': {Action: "select-window", Args: []string{"6"}},
			'7': {Action: "select-window", Args: []string{"7"}},
			'8': {Action: "select-window", Args: []string{"8"}},
			'9': {Action: "select-window", Args: []string{"9"}},
			'd': {Action: "detach"},
			'r': {Action: "reload"},
			'P': {Action: "toggle-lead"},
		},
	}
}
