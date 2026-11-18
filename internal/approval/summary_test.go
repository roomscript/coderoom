package approval

import "testing"

func TestCommandSummary(t *testing.T) {
	tests := []struct{ command, want string }{
		{"rg -n approval internal/", "rg"}, {"/opt/tools/deploy --force", "deploy"},
		{"git diff --check", "git diff"}, {"git -C repo -c core.pager=cat diff", "git diff"},
		{"git --git-dir=repo --no-pager status", "git status"}, {"git --unknown diff", "shell command"},
		{"make test", "make test"}, {"make -C repo -f Othermakefile test", "make test"},
		{"make -j4 test", "make test"}, {"make --directory=repo test", "make test"},
		{"make test lint", "make"}, {"make -n", "make"}, {"make -C", "shell command"},
		{"make --unknown test", "shell command"}, {"make -j test", "shell command"},
		{"/bin/zsh -lc 'git diff --check'", "git diff"}, {"sh -c 'make test'", "make test"},
		{"bash --noprofile --norc -c 'git status'", "git status"}, {"zsh -f -c 'rg foo'", "rg"},
		{"bash script.sh", "shell command"}, {"bash -c '$COMMAND'", "shell command"},
		{"bash -c 'git diff | head'", "shell command"}, {"bash -c 'echo \"'", "shell command"},
		{"git diff | head", "shell command"}, {"cd repo && make test", "shell command"},
		{"git diff || true", "shell command"}, {"git diff; echo ok", "shell command"},
		{"git diff;", "shell command"}, {"git diff > out", "shell command"},
		{"git diff &", "shell command"}, {"echo \"$(deploy)\"", "shell command"},
		{"echo `deploy`", "shell command"}, {"echo <(deploy)", "shell command"},
		{"echo $HOME", "shell command"}, {"A=b git diff", "shell command"},
		{"(git diff)", "shell command"}, {"echo '", "shell command"}, {"", "shell command"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := CommandSummary(tt.command); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
