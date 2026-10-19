package cli

import "testing"

// The remote command is a single string, so the quoting has to survive the shell
// over there rather than the one here.
func TestShellJoinQuotesOnlyWhatNeedsIt(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"systemctl", "status", "api"}, "systemctl status api"},
		{[]string{"ls", "-la", "/var/www/site"}, "ls -la /var/www/site"},
		{[]string{"echo", "hello world"}, "echo 'hello world'"},
		{[]string{"sh", "-c", "echo 'hi'"}, `sh -c 'echo '\''hi'\'''`},
		{[]string{"echo", ""}, "echo ''"},
	}
	for _, tc := range cases {
		if got := shellJoin(tc.args); got != tc.want {
			t.Errorf("shellJoin(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
