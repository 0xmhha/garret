package diskcrypt

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCheck(t *testing.T) {
	fileVault := func(out string, err error) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return out, err }
	}
	crypt := fstest.MapFS{
		"sys/block/dm-0/dm/uuid": {Data: []byte("LVM-abc\n")},
		"sys/block/dm-1/dm/uuid": {Data: []byte("CRYPT-LUKS2-1234-root\n")},
	}
	plain := fstest.MapFS{"sys/block/dm-0/dm/uuid": {Data: []byte("LVM-abc\n")}}
	tests := []struct {
		name string
		p    Probe
		want State
	}{
		{"macOS on", Probe{GOOS: "darwin", FileVault: fileVault("FileVault is On.\n", nil)}, On},
		{"macOS off", Probe{GOOS: "darwin", FileVault: fileVault("FileVault is Off.\n", nil)}, Off},
		{"macOS unknown", Probe{GOOS: "darwin", FileVault: fileVault("", errors.New("not found"))}, Unknown},
		{"linux crypt", Probe{GOOS: "linux", Root: crypt}, On},
		{"linux none", Probe{GOOS: "linux", Root: plain}, Off},
		{"linux no sysfs", Probe{GOOS: "linux", Root: fstest.MapFS{}}, Off},
		{"other system", Probe{GOOS: "plan9"}, Unknown},
	}
	for _, tt := range tests {
		if got := Check(context.Background(), tt.p); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCheckLinuxUnreadable(t *testing.T) {
	if got := Check(context.Background(), Probe{GOOS: "linux", Root: badFS{}}); got != Unknown {
		t.Fatalf("unreadable sysfs: %v", got)
	}
}

type badFS struct{}

func (badFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }
