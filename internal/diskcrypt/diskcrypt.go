package diskcrypt

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
)

// State is what the check found.
type State int

// The results of Check.
const (
	Unknown State = iota // the check could not tell
	On                   // disk encryption appears to be on
	Off                  // disk encryption appears to be off
)

func (s State) String() string {
	switch s {
	case On:
		return "on"
	case Off:
		return "off"
	}
	return "unknown"
}

const (
	goosDarwin   = "darwin"
	goosLinux    = "linux"
	fileVaultOn  = "FileVault is On"
	blockDir     = "sys/block"
	cryptUUIDTag = "CRYPT-"
)

// Probe is how Check looks at the system. Production passes runtime.GOOS,
// a function running `fdesetup status`, and os.DirFS("/").
type Probe struct {
	GOOS      string
	FileVault func(ctx context.Context) (string, error)
	Root      fs.FS
}

// Check reports whether disk encryption appears to be on: FileVault on
// macOS, a dm-crypt device on Linux. It never fails; it returns Unknown
// when it cannot tell.
func Check(ctx context.Context, p Probe) State {
	switch p.GOOS {
	case goosDarwin:
		if p.FileVault == nil {
			return Unknown
		}
		out, err := p.FileVault(ctx)
		if err != nil {
			return Unknown
		}
		if strings.Contains(out, fileVaultOn) {
			return On
		}
		return Off
	case goosLinux:
		if p.Root == nil {
			return Unknown
		}
		return checkLinux(p.Root)
	}
	return Unknown
}

func checkLinux(root fs.FS) State {
	entries, err := fs.ReadDir(root, blockDir)
	if errors.Is(err, fs.ErrNotExist) {
		return Off
	}
	if err != nil {
		return Unknown
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "dm-") {
			continue
		}
		uuid, err := fs.ReadFile(root, path.Join(blockDir, e.Name(), "dm", "uuid"))
		if err == nil && strings.HasPrefix(string(uuid), cryptUUIDTag) {
			return On
		}
	}
	return Off
}
