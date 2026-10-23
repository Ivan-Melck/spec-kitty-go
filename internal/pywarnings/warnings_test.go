package pywarnings

import "testing"

func TestRegistryAndCapture(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	var emitted []Record
	sink := func(module string, line int, category, message, source string) {
		emitted = append(emitted, Record{module, category, message, source, line})
	}
	warn := func(module string) { Warn(sink, module, 12, "UserWarning", "broken pack", "resolve()") }
	warn("offering.py")
	warn("offering.py")
	warn("provenance.py")
	if len(emitted) != 2 {
		t.Fatalf("default action emitted %d records, want one per caller", len(emitted))
	}
	var inner []Record
	outer := Capture(func() {
		warn("offering.py")
		warn("offering.py")
		inner = Capture(func() { warn("provenance.py") })
		warn("offering.py")
	})
	if len(outer) != 3 || len(inner) != 1 || len(emitted) != 2 {
		t.Fatalf("capture: outer=%v inner=%v emitted=%v", outer, inner, emitted)
	}
	if outer[0] != emitted[0] || inner[0] != emitted[1] {
		t.Fatalf("capture lost warning fields: outer=%v inner=%v", outer, inner)
	}
	warn("offering.py")
	if len(emitted) != 3 {
		t.Fatal("leaving capture did not invalidate the caller registry")
	}
}

func TestCapturePanicRestoresRegistry(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	func() {
		defer func() {
			if recover() != "load failed" {
				t.Fatal("capture did not propagate the load panic")
			}
		}()
		Capture(func() { panic("load failed") })
	}()
	emitted := false
	Warn(func(string, int, string, string, string) { emitted = true }, "pack.py", 1, "UserWarning", "broken pack", "resolve()")
	if !emitted {
		t.Fatal("panic left warning capture active")
	}
}
