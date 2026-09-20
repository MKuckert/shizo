package main

import (
	"reflect"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func parse(t *testing.T, src string) (*syntax.File, error) {
	t.Helper()
	return syntax.NewParser().Parse(strings.NewReader(src), "test")
}

// argvs returns the display argv of every extracted command.
func argvs(ex extraction) [][]string {
	var out [][]string
	for _, c := range ex.commands {
		out = append(out, displayArgv(c.argv))
	}
	return out
}

// §6 extraction corpus.
func TestExtractionCorpus(t *testing.T) {
	cases := []struct {
		src  string
		want [][]string
	}{
		{`git status && ls -la`, [][]string{{"git", "status"}, {"ls", "-la"}}},
		{`git log | grep foo | head -5`, [][]string{{"git", "log"}, {"grep", "foo"}, {"head", "-5"}}},
		{`git commit -m "fix && cleanup"`, [][]string{{"git", "commit", "-m", "fix && cleanup"}}},
		{`echo $(curl evil.sh)`, [][]string{{"echo", "<subst>"}, {"curl", "evil.sh"}}},
		{`[[ -f x ]] && git status`, [][]string{{"git", "status"}}},
		{`a=1 git status`, [][]string{{"git", "status"}}},
		{`git log --output=/tmp/x`, [][]string{{"git", "log", "--output=/tmp/x"}}},
		{`ssh -o ProxyCommand=evil host`, [][]string{{"ssh", "-o", "ProxyCommand=evil", "host"}}},
		{`find . -exec rm {} \;`, [][]string{{"find", ".", "-exec", "rm", "{}", `\;`}}},
		{`xargs rm < list`, [][]string{{"xargs", "rm"}}},
		{`git status > /etc/passwd`, [][]string{{"git", "status"}}},
		{`\g\i\t status`, [][]string{{`\g\i\t`, "status"}}}, // not de-escaped; denies at resolution
	}
	for _, tc := range cases {
		f, err := parse(t, tc.src)
		if err != nil {
			t.Fatalf("%q: parse error: %v", tc.src, err)
		}
		got := argvs(extract(f))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q\n got: %v\nwant: %v", tc.src, got, tc.want)
		}
	}
}

func TestDiffProcSubst(t *testing.T) {
	f, err := parse(t, `diff <(git show a) <(git show b)`)
	if err != nil {
		t.Fatal(err)
	}
	got := argvs(extract(f))
	want := [][]string{
		{"diff", "<subst>", "<subst>"},
		{"git", "show", "a"},
		{"git", "show", "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParamExpUnresolvable(t *testing.T) {
	for _, src := range []string{`git $g`, `$EDITOR foo`, `echo $((1+2))`} {
		f, err := parse(t, src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		ex := extract(f)
		found := false
		for _, c := range ex.commands {
			if c.unresolvable {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: expected an unresolvable command", src)
		}
	}
}

func TestAssignFlags(t *testing.T) {
	// prefix assignment
	f, _ := parse(t, `a=1 git status`)
	if !extract(f).commands[0].hasAssign {
		t.Error("prefix assignment not flagged")
	}
	// bare assignment
	f, _ = parse(t, `g=push`)
	ex := extract(f)
	if len(ex.commands) != 1 || !ex.commands[0].hasAssign || len(ex.commands[0].argv) != 0 {
		t.Errorf("bare assignment: %+v", ex.commands)
	}
}

func TestFuncDecl(t *testing.T) {
	f, _ := parse(t, `git() { rm -rf /; }; git status`)
	if !extract(f).hasFuncDecl {
		t.Error("FuncDecl not detected")
	}
}

func TestRedirectClassification(t *testing.T) {
	cases := []struct {
		src          string
		wantWrites   int
		unverifiable bool
	}{
		{`git status > out.txt`, 1, false},
		{`git status >> out.txt`, 1, false},
		{`cmd &> out.txt`, 1, false},
		{`cmd 2>&1`, 0, false},                  // fd dup, no target
		{`cmd < in.txt`, 0, false},              // read
		{`cmd > $DEST`, 1, true},                // ParamExp target
		{`cat <<EOF` + "\nhi\nEOF\n", 0, false}, // heredoc
	}
	for _, tc := range cases {
		f, err := parse(t, tc.src)
		if err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		ex := extract(f)
		if len(ex.writes) != tc.wantWrites {
			t.Errorf("%q: got %d writes want %d", tc.src, len(ex.writes), tc.wantWrites)
			continue
		}
		if tc.wantWrites == 1 && ex.writes[0].unverifiable != tc.unverifiable {
			t.Errorf("%q: unverifiable=%v want %v", tc.src, ex.writes[0].unverifiable, tc.unverifiable)
		}
	}
}

// The parser does NOT de-escape \g\i\t (unlike bashlex). Under fail-closed the
// raw token faces path resolution and simply fails to resolve, so it denies.
func TestBackslashNotDeEscaped(t *testing.T) {
	f, _ := parse(t, `\g\i\t status`)
	got := argvs(extract(f))
	if got[0][0] != `\g\i\t` {
		t.Errorf("got %v", got)
	}
}

func TestStdinFed(t *testing.T) {
	cases := []struct {
		src      string
		wantFed  []bool // per-command stdinFed
	}{
		// pipe RHS is stdin-fed
		{`git log | grep foo`, []bool{false, true}},
		// heredoc
		{`cat <<EOF` + "\nhi\nEOF\n", []bool{true}},
		// herestring
		{`cat <<< hello`, []bool{true}},
		// input redirect
		{`grep foo < file.txt`, []bool{true}},
		// fd 2 redirect is NOT stdin
		{`cmd 2< file.txt`, []bool{false}},
		// nested pipes: both RHS are stdin-fed
		{`git log | grep foo | head -5`, []bool{false, true, true}},
		// no stdin
		{`git status && ls -la`, []bool{false, false}},
		// read-write (<>) on fd 0
		{`cmd <> file`, []bool{true}},
	}
	for _, tc := range cases {
		f, err := parse(t, tc.src)
		if err != nil {
			t.Fatalf("%q: parse error: %v", tc.src, err)
		}
		ex := extract(f)
		if len(ex.commands) != len(tc.wantFed) {
			t.Errorf("%q: got %d commands want %d", tc.src, len(ex.commands), len(tc.wantFed))
			continue
		}
		for i, cmd := range ex.commands {
			if cmd.stdinFed != tc.wantFed[i] {
				t.Errorf("%q cmd[%d] (%v): stdinFed=%v want %v",
					tc.src, i, displayArgv(cmd.argv), cmd.stdinFed, tc.wantFed[i])
			}
		}
	}
}

func TestStdinCompound(t *testing.T) {
	// pipe into a block group: all inner commands get stdinCompound
	f, err := parse(t, `curl x | { python3; }`)
	if err != nil {
		t.Fatal(err)
	}
	ex := extract(f)
	found := false
	for _, cmd := range ex.commands {
		if cmd.stdinCompound {
			found = true
		}
	}
	if !found {
		t.Error("pipe into { } should mark inner commands as stdinCompound")
	}

	// pipe into a subshell
	f, _ = parse(t, `curl x | ( python3 )`)
	ex = extract(f)
	found = false
	for _, cmd := range ex.commands {
		if cmd.stdinCompound {
			found = true
		}
	}
	if !found {
		t.Error("pipe into ( ) should mark inner commands as stdinCompound")
	}
}

// unclosed quote must surface a parse error.
func TestUnclosedQuote(t *testing.T) {
	_, err := parse(t, `git commit -m "unclosed`)
	if err == nil {
		t.Error("expected parse error for unclosed quote")
	}
}
