package remoteinfo

import "testing"

// The output of the three commands on a real Linux host, including df's header.
const linuxSample = `0.52 0.41 0.30 2/345 6789
Mem:  8154099712 3221225472 1234567890 12345 789012345 4932874240
/dev/sda1  102400000 40960000 61440000  40% /
8
`

func TestParseLinuxSample(t *testing.T) {
	info := Parse(linuxSample)
	if !info.Known {
		t.Fatal("a full answer should be known")
	}
	if info.Load != 0.52 {
		t.Errorf("load = %v, want 0.52", info.Load)
	}
	if info.CPUs != 8 {
		t.Errorf("CPUs = %d, want 8", info.CPUs)
	}
	if info.MemTotal != 8154099712 || info.MemUsed != 3221225472 {
		t.Errorf("memory = %d/%d", info.MemUsed, info.MemTotal)
	}
	if info.DiskTotal != 102400000*1024 || info.DiskFree != 61440000*1024 {
		t.Errorf("disk = %d total, %d free", info.DiskTotal, info.DiskFree)
	}
	if got := info.LoadPercent(); got != 6 { // 0.52/8 = 6.5%
		t.Errorf("load percent = %d, want 6", got)
	}
	if got := info.MemPercent(); got != 39 {
		t.Errorf("memory percent = %d, want 39", got)
	}
	if got := info.DiskPercent(); got != 40 {
		t.Errorf("disk percent = %d, want 40", got)
	}
}

// A host that answers nothing must say so rather than show zeroes that look like
// a healthy machine.
func TestParseUnknown(t *testing.T) {
	info := Parse("")
	if info.Known {
		t.Error("an empty answer should not be known")
	}
	if info.Err == "" {
		t.Error("an unknown answer should carry a reason")
	}
}

// A host that answers two of three is still useful: the numbers present are real.
func TestParsePartial(t *testing.T) {
	info := Parse("Mem: 1000 250 750 0 0 0\n")
	if !info.Known || info.MemTotal != 1000 || info.MemUsed != 250 {
		t.Fatalf("memory should be read from a partial answer: %+v", info)
	}
	if info.CPUs != 1 {
		t.Errorf("a missing CPU count should default to 1, got %d", info.CPUs)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/var/www/it's here"); got != `'/var/www/it'\''s here'` {
		t.Errorf("shellQuote = %s", got)
	}
}
