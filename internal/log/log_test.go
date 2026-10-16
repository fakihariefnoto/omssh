package log

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// A credential typed into a prompt is a Secret, and a Secret cannot be printed
// as anything but "[redacted]" no matter how it is formatted.
func TestSecretCannotBeFormatted(t *testing.T) {
	secret := Secret("hunter2")
	for name, got := range map[string]string{
		"String":   secret.String(),
		"GoString": secret.GoString(),
		"Format":   strings.TrimSpace(slog.Any("s", secret).Value.String()),
		"LogValue": secret.LogValue().String(),
	} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("%s leaked the secret: %q", name, got)
		}
		if !strings.Contains(got, redacted) {
			t.Errorf("%s should redact, got %q", name, got)
		}
	}
	if secret.Reveal() != "hunter2" {
		t.Error("Reveal should return the value; it is the one audited escape hatch")
	}
	encoded, err := secret.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hunter2") {
		t.Errorf("JSON encoding leaked the secret: %s", encoded)
	}
}

// The redactor covers the case the type cannot: a password that arrives as a
// plain string from a prompt or a config file.
func TestRedactorScrubsValuesAnywhere(t *testing.T) {
	var buf bytes.Buffer
	redactor := NewRedactor()
	redactor.Add("s3cret-passphrase")
	logger := New(Options{Writer: &buf, Level: slog.LevelDebug, Redactor: redactor})

	logger.Info("connecting as deploy with s3cret-passphrase")
	logger.Info("auth", "passphrase", "s3cret-passphrase", "user", "deploy")
	logger.With("key", "s3cret-passphrase").Warn("retrying")

	out := buf.String()
	if strings.Contains(out, "s3cret-passphrase") {
		t.Fatalf("the secret reached the log:\n%s", out)
	}
	if !strings.Contains(out, redacted) {
		t.Errorf("expected a redaction marker, got:\n%s", out)
	}
	if !strings.Contains(out, "deploy") {
		t.Errorf("redaction should not remove everything else:\n%s", out)
	}
}

// Very short values are ignored: scrubbing every "a" would make the log useless.
func TestRedactorIgnoresTinyValues(t *testing.T) {
	var buf bytes.Buffer
	redactor := NewRedactor()
	redactor.Add("a", "")
	logger := New(Options{Writer: &buf, Level: slog.LevelDebug, Redactor: redactor})
	logger.Info("a normal message")

	if strings.Contains(buf.String(), redacted) {
		t.Errorf("short values should not be scrubbed:\n%s", buf.String())
	}
}

// Without registered secrets the logger is a thin wrapper, and nothing is
// mangled on the way through.
func TestLoggerWithoutRedactorPassesThrough(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Options{Writer: &buf, Level: slog.LevelInfo})
	logger.Info("plain message", "count", 3)

	out := buf.String()
	if !strings.Contains(out, "plain message") || !strings.Contains(out, "count=3") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Options{Writer: &buf, Level: slog.LevelWarn})
	logger.Info("should not appear")
	logger.Warn("should appear")
	if strings.Contains(buf.String(), "should not appear") {
		t.Error("the level filter did not apply")
	}
	if !strings.Contains(buf.String(), "should appear") {
		t.Error("the warning was dropped")
	}
}

// Groups keep their structure while their string values are still scrubbed.
func TestRedactionInsideGroups(t *testing.T) {
	var buf bytes.Buffer
	redactor := NewRedactor()
	redactor.Add("topsecretvalue")
	logger := New(Options{Writer: &buf, Level: slog.LevelInfo, Redactor: redactor})
	logger.Info("auth", slog.Group("ssh", "password", "topsecretvalue", "user", "deploy"))

	if strings.Contains(buf.String(), "topsecretvalue") {
		t.Fatalf("a grouped value leaked:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "deploy") {
		t.Errorf("the rest of the group should survive:\n%s", buf.String())
	}
}
