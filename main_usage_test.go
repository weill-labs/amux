package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMainUsageHelperIsolatesAmbientSessionEnv(t *testing.T) {
	t.Parallel()

	cmd := newHermeticMainCmd(t, "kill")

	if !strings.Contains(strings.Join(cmd.Args, "\x00"), "\x00-s\x00"+hermeticMainSession(t.Name())+"\x00kill") {
		t.Fatalf("helper args = %q, want injected isolated session before command", cmd.Args)
	}
	if got, want := strings.Join(cmd.Env, "\x00"), strings.Join(hermeticMainEnv(), "\x00"); got != want {
		t.Fatalf("helper env = %q, want %q", cmd.Env, hermeticMainEnv())
	}
	for _, prefix := range []string{"AMUX_PANE=", "AMUX_SESSION=", "TMUX="} {
		for _, entry := range cmd.Env {
			if strings.HasPrefix(entry, prefix) {
				t.Fatalf("helper env leaked %s in %q", prefix, entry)
			}
		}
	}
}

func TestMainSendKeysUsageIncludesWaitIdleFlags(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "send-keys", "pane-1")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux send-keys <pane> [--wait idle|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...") {
		t.Fatalf("usage output missing wait-idle flags:\n%s", out)
	}
}

func TestMainKeyCommandsHelpFlagsPrintUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "send-keys long help",
			args: []string{"send-keys", "pane-1", "--help"},
			want: "usage: amux send-keys <pane> [--wait idle|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...",
		},
		{
			name: "send-keys short help",
			args: []string{"send-keys", "pane-1", "-h"},
			want: "usage: amux send-keys <pane> [--wait idle|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...",
		},
		{
			name: "type-keys long help",
			args: []string{"type-keys", "--help"},
			want: "usage: amux type-keys [--wait ui=input-idle] [--timeout <duration>] [--hex] <keys>...",
		},
		{
			name: "type-keys short help",
			args: []string{"type-keys", "-h"},
			want: "usage: amux type-keys [--wait ui=input-idle] [--timeout <duration>] [--hex] <keys>...",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, code := runHermeticMain(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s", code, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("usage output = %q, want substring %q", out, tt.want)
			}
			if strings.Contains(out, "connecting to server") {
				t.Fatalf("help flag should not dispatch to the server:\n%s", out)
			}
		})
	}
}

func TestMainWaitUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "wait")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux wait <idle|busy|exited|content|layout|clipboard|checkpoint|ui> ...") {
		t.Fatalf("wait usage output = %q", out)
	}
}

func TestMainKVCommandsHelpFlagsPrintUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "set-kv long help",
			args: []string{"set-kv", "pane-1", "--help"},
			want: "usage: amux set-kv <pane> key=value [key=value...]",
		},
		{
			name: "set-kv short help",
			args: []string{"set-kv", "pane-1", "-h"},
			want: "usage: amux set-kv <pane> key=value [key=value...]",
		},
		{
			name: "get-kv long help",
			args: []string{"get-kv", "pane-1", "--help"},
			want: "usage: amux get-kv <pane> [key...]",
		},
		{
			name: "get-kv short help",
			args: []string{"get-kv", "pane-1", "-h"},
			want: "usage: amux get-kv <pane> [key...]",
		},
		{
			name: "rm-kv long help",
			args: []string{"rm-kv", "pane-1", "--help"},
			want: "usage: amux rm-kv <pane> key [key...]",
		},
		{
			name: "rm-kv short help",
			args: []string{"rm-kv", "pane-1", "-h"},
			want: "usage: amux rm-kv <pane> key [key...]",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, code := runHermeticMain(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s", code, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("usage output = %q, want substring %q", out, tt.want)
			}
			if strings.Contains(out, "connecting to server") {
				t.Fatalf("help flag should not dispatch to the server:\n%s", out)
			}
		})
	}
}

func TestMainCursorUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "cursor")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux cursor <layout|clipboard|ui> [--client <id>]") {
		t.Fatalf("cursor usage output = %q", out)
	}
}

func TestMainRemovedCommandsAreUnknown(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"set-hook", "unset-hook", "list-hooks", "delegate"} {
		out, code := runHermeticMain(t, command)
		if code != 1 {
			t.Fatalf("%s exit code = %d, want 1\n%s", command, code, out)
		}
		if !strings.Contains(out, "amux: unknown command \""+command+"\"") {
			t.Fatalf("%s output = %q", command, out)
		}
	}
}

func TestMainCopyModeDispatchesWithoutExplicitPane(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "copy-mode")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux copy-mode") {
		t.Fatalf("copy-mode should dispatch without a pane argument, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "copy-mode")
}

func TestMainCursorDispatchesWhenKindProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "cursor", "layout")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux cursor") {
		t.Fatalf("cursor should dispatch when a kind is provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "cursor")
}

func TestMainWaitDispatchesWhenKindProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "wait", "layout")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux wait") {
		t.Fatalf("wait should dispatch when a kind is provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "wait")
}

func TestMainTypeKeysDispatchesWhenKeysProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "type-keys", "abc")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux type-keys") {
		t.Fatalf("type-keys should dispatch when keys are provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "type-keys")
}

func TestMainTypeKeysWarnsWhenFirstArgLooksLikePaneRef(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"type-keys", "pane-1"},
		{"type-keys", "pane-1", "hello"},
	}

	for _, args := range tests {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()

			out, code := runHermeticMain(t, args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\n%s", code, out)
			}
			if !strings.Contains(out, `warning: "pane-1" looks like a pane ref`) ||
				!strings.Contains(out, `use send-keys pane-1 ...`) {
				t.Fatalf("type-keys pane-ref warning missing send-keys hint:\n%s", out)
			}
			assertMainCommandConnectError(t, out, "type-keys")
		})
	}
}

func TestLooksLikePaneRefArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arg  string
		want bool
	}{
		{arg: "pane-1", want: true},
		{arg: "7", want: true},
		{arg: "pane-one", want: true},
		{arg: "task", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.arg, func(t *testing.T) {
			t.Parallel()
			if got := looksLikePaneRefArg(tt.arg); got != tt.want {
				t.Fatalf("looksLikePaneRefArg(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestMainTypeKeysUsageIncludesWaitFlags(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "type-keys")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux type-keys [--wait ui=input-idle] [--timeout <duration>] [--hex] <keys>...") {
		t.Fatalf("type-keys usage output missing wait flags:\n%s", out)
	}
}

func TestMainMoveUpDownUsageAndDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		args            []string
		wantUsage       string
		wantConnectName string
	}{
		{
			name:      "move-up usage",
			args:      []string{"move-up"},
			wantUsage: "usage: amux move-up <pane>",
		},
		{
			name:            "move-up dispatch",
			args:            []string{"move-up", "pane-1"},
			wantConnectName: "move-up",
		},
		{
			name:      "move-down usage",
			args:      []string{"move-down"},
			wantUsage: "usage: amux move-down <pane>",
		},
		{
			name:            "move-down dispatch",
			args:            []string{"move-down", "pane-1"},
			wantConnectName: "move-down",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, code := runHermeticMain(t, tt.args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\n%s", code, out)
			}
			if tt.wantUsage != "" {
				if !strings.Contains(out, tt.wantUsage) {
					t.Fatalf("usage output = %q, want substring %q", out, tt.wantUsage)
				}
				return
			}
			if strings.Contains(out, "usage: amux "+tt.wantConnectName) {
				t.Fatalf("%s should dispatch when a pane is provided, got usage output:\n%s", tt.wantConnectName, out)
			}
			assertMainCommandConnectError(t, out, tt.wantConnectName)
		})
	}
}

func TestMainHelpIncludesMoveUpDown(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "amux [-s session] move-up <pane>") {
		t.Fatalf("help output missing move-up:\n%s", out)
	}
	if !strings.Contains(out, "amux [-s session] move-down <pane>") {
		t.Fatalf("help output missing move-down:\n%s", out)
	}
}

func TestMainHelpOmitsDelegate(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "amux [-s session] delegate <pane>") {
		t.Fatalf("help output should omit delegate:\n%s", out)
	}
}

func TestMainHelpIncludesWaitCheckpoint(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "amux [-s session] wait checkpoint [--after N] [--timeout 15s]") {
		t.Fatalf("help output missing wait-checkpoint:\n%s", out)
	}
}

func TestMainAllCommandsSupportLongHelp(t *testing.T) {
	t.Parallel()

	for _, command := range mainDispatchCommands(t) {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			out, code, timedOut := runHermeticMainWithTimeout(t, 2*time.Second, command, "--help")
			if timedOut {
				t.Fatalf("%s --help timed out\n%s", command, out)
			}
			if code != 0 {
				t.Fatalf("%s --help exit code = %d, want 0\n%s", command, code, out)
			}
			want := "usage: amux " + command
			if !strings.Contains(out, want) {
				t.Fatalf("%s --help output = %q, want substring %q", command, out, want)
			}
			if strings.Contains(out, "connecting to server") {
				t.Fatalf("%s --help should not dispatch to the server:\n%s", command, out)
			}
		})
	}
}

func mainDispatchCommands(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(main.go): %v", err)
	}

	commands := map[string]struct{}{}
	var mainBody *ast.BlockStmt
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		mainBody = fn.Body
		break
	}
	if mainBody == nil {
		t.Fatal("main function not found")
	}

	ast.Inspect(mainBody, func(n ast.Node) bool {
		switchStmt, ok := n.(*ast.SwitchStmt)
		if !ok || !isArgsIndexZeroExpr(switchStmt.Tag) {
			return true
		}
		for _, stmt := range switchStmt.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				name := strings.Trim(lit.Value, `"`)
				switch name {
				case "help", "--help", "-h":
					continue
				}
				commands[name] = struct{}{}
			}
		}
		return false
	})

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func isArgsIndexZeroExpr(expr ast.Expr) bool {
	indexExpr, ok := expr.(*ast.IndexExpr)
	if !ok {
		return false
	}
	ident, ok := indexExpr.X.(*ast.Ident)
	if !ok || ident.Name != "args" {
		return false
	}
	lit, ok := indexExpr.Index.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}

func TestMainKillAllowsImplicitActivePane(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "kill")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux kill") {
		t.Fatalf("kill should accept an omitted pane, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "kill")
}

func TestMainKillUsageRejectsTimeoutWithoutCleanup(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "kill", "--timeout", "1s")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux kill [--cleanup] [--timeout <duration>] [pane]") {
		t.Fatalf("kill usage output = %q", out)
	}
}

func TestMainResetUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "reset")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux reset <pane>") {
		t.Fatalf("reset usage output = %q", out)
	}
}

func TestMainSetKVDispatchesWhenPaneAndPairsProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "set-kv", "pane-1", "foo=bar")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux set-kv") {
		t.Fatalf("set-kv should dispatch when pane and kv pairs are provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "set-kv")
}

func TestMainGetKVDispatchesWhenPaneProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "get-kv", "pane-1")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux get-kv") {
		t.Fatalf("get-kv should dispatch when a pane is provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "get-kv")
}

func TestMainRmKVDispatchesWhenPaneAndKeysProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "rm-kv", "pane-1", "foo")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux rm-kv") {
		t.Fatalf("rm-kv should dispatch when pane and keys are provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "rm-kv")
}

func TestMainRefreshMetaIsUnknownCommand(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "refresh-meta")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, `amux: unknown command "refresh-meta"`) {
		t.Fatalf("refresh-meta output = %q", out)
	}
}
