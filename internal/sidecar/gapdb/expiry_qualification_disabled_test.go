//go:build !spec_kitty_expiry_host_qualification || !linux || !amd64

package gapdb

import (
	"os"
	"reflect"
	"testing"
	"unsafe"
)

func TestExpiryLaunchDisabledEmptyFieldsAndOrdinaryCommand(t *testing.T) {
	if unsafe.Sizeof(verifiedExpiryState{}) != 0 || unsafe.Sizeof(lifecycleExpiryState{}) != 0 || unsafe.Sizeof(expiryCleanupReport{}) != 0 || unsafe.Sizeof(expiryLifecycleToken{}) != 0 || unsafe.Sizeof(expiryLifecycleTeardown{}) != 0 {
		t.Fatal("disabled conditional state is not empty")
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(VerifiedExecutable{}), reflect.TypeOf(Lifecycle{})} {
		if typ.Field(0).Name != "expiry" || typ.Field(0).Offset != 0 || typ.Field(0).Type.Size() != 0 {
			t.Fatal("conditional field not first/empty", typ)
		}
		fields := make([]reflect.StructField, typ.NumField()-1)
		for i := 1; i < typ.NumField(); i++ {
			fields[i-1] = typ.Field(i)
		}
		originalShape := reflect.StructOf(fields)
		if typ.Size() != originalShape.Size() || typ.Align() != originalShape.Align() {
			t.Fatal("ordinary geometry differs from original field body", typ)
		}
		for i := 1; i < typ.NumField(); i++ {
			if typ.Field(i).Offset != originalShape.Field(i-1).Offset {
				t.Fatal("ordinary offset changed", typ, typ.Field(i).Name)
			}
		}
	}
	var report *expiryCleanupReport
	report.sourceClosed(nil)
	report.imageClosed(nil)
	v := &VerifiedExecutable{}
	if v.expiry.cleanupReport() != nil || v.closeExpiryUnconsumed() != nil {
		t.Fatal("disabled hooks retained a report/resource")
	}
	f, err := os.CreateTemp(t.TempDir(), "ordinary-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	v.execution = f
	env := []string{"LANG=C", "TZ=UTC"}
	command, err := v.fixedCommand([]string{"unit-only-no-exec"}, env)
	if err != nil || command.Path != "/proc/self/fd/3" || !reflect.DeepEqual(command.ExtraFiles, []*os.File{f}) || !reflect.DeepEqual(command.Env, env) {
		t.Fatal("ordinary command mapping changed", err)
	}
	env[0] = "mutated"
	if command.Env[0] != "LANG=C" {
		t.Fatal("ordinary environment alias")
	}
	if !(&Lifecycle{}).legacyEvidenceAllowed() || !(&Lifecycle{}).ordinaryRestartAllowed() || !expiryAttachAllowed(SidecarProvenance{}) {
		t.Fatal("disabled hook refused ordinary operation")
	}
}
