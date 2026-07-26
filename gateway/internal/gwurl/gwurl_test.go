package gwurl

import "testing"

// clear blanks every key the resolver consults, so a variable already set in
// the developer's shell can't make a case pass or fail spuriously.
func clear(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
}

func TestDefaultWhenUnset(t *testing.T) {
	clear(t)
	if got := URL(); got != DefaultURL {
		t.Errorf("URL() = %q, want %q", got, DefaultURL)
	}
}

// Each supported name works on its own, including the two legacy ones.
func TestEachKeyIsHonored(t *testing.T) {
	for _, key := range envKeys {
		t.Run(key, func(t *testing.T) {
			clear(t)
			t.Setenv(key, "http://host:9999")
			if got := URL(); got != "http://host:9999" {
				t.Errorf("with %s set, URL() = %q", key, got)
			}
		})
	}
}

// When several are set, the most current name wins.
func TestPrecedence(t *testing.T) {
	clear(t)
	t.Setenv("DISPATCH_URL", "http://oldest:3")
	if got := URL(); got != "http://oldest:3" {
		t.Fatalf("URL() = %q, want the only set key", got)
	}

	t.Setenv("CREWCHIEF_URL", "http://middle:2")
	if got := URL(); got != "http://middle:2" {
		t.Errorf("URL() = %q, want CREWCHIEF_URL to beat DISPATCH_URL", got)
	}

	t.Setenv("CHAIO_CREWCHIEF_URL", "http://current:1")
	if got := URL(); got != "http://current:1" {
		t.Errorf("URL() = %q, want CHAIO_CREWCHIEF_URL to beat all", got)
	}
}
