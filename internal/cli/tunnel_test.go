package cli

import "testing"

// The ssh -L shape: [bind:]port:host:port, with loopback as the safe default.
func TestParseForward(t *testing.T) {
	cases := []struct {
		spec   string
		listen string
		target string
	}{
		{"8080:db.internal:5432", "127.0.0.1:8080", "db.internal:5432"},
		{"0.0.0.0:8080:db.internal:5432", "0.0.0.0:8080", "db.internal:5432"},
		{"127.0.0.1:80:web:80", "127.0.0.1:80", "web:80"},
	}
	for _, tc := range cases {
		spec, err := parseForward(tc.spec, "local")
		if err != nil {
			t.Fatalf("parseForward(%q): %v", tc.spec, err)
		}
		if spec.listen != tc.listen || spec.target != tc.target {
			t.Errorf("parseForward(%q) = %s → %s", tc.spec, spec.listen, spec.target)
		}
	}
	if _, err := parseForward("8080", "local"); err == nil {
		t.Error("a bare port is not a local forward")
	}
}

func TestParseDynamic(t *testing.T) {
	spec, err := parseDynamic("1080")
	if err != nil || spec.listen != "127.0.0.1:1080" || spec.kind != "dynamic" {
		t.Errorf("parseDynamic(1080) = %+v, %v", spec, err)
	}
	spec, err = parseDynamic("0.0.0.0:1080")
	if err != nil || spec.listen != "0.0.0.0:1080" {
		t.Errorf("parseDynamic(bind) = %+v, %v", spec, err)
	}
	if _, err := parseDynamic(""); err == nil {
		t.Error("an empty dynamic spec should be refused")
	}
}
