package main

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/0xmhha/garret/internal/config"
)

// memFiles is a fileStore in memory; failWrite makes every write fail.
type memFiles struct {
	files     map[string][]byte
	failWrite bool
}

func (m *memFiles) ReadFile(rel string) ([]byte, error) {
	b, ok := m.files[rel]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return b, nil
}

func (m *memFiles) WriteFile(rel string, data []byte) error {
	if m.failWrite {
		return errors.New("disk full")
	}
	m.files[rel] = data
	return nil
}

func TestResolveClientCerts(t *testing.T) {
	errAsk := errors.New("stdin closed")
	answer := func(m joinMode) func() (joinMode, error) { return func() (joinMode, error) { return m, nil } }
	tests := []struct {
		name      string
		flagSet   bool
		flagCerts bool
		stored    string // "" means no kept mode
		ask       func() (joinMode, error)
		failWrite bool
		want      bool
		wantKept  string
		wantErr   bool
	}{
		{name: "flag on wins over a kept browser mode", flagSet: true, flagCerts: true, stored: "browser", want: true, wantKept: "browser"},
		{name: "flag off wins over a kept enroll mode", flagSet: true, stored: "enroll", want: false, wantKept: "enroll"},
		{name: "kept enroll", stored: "enroll", want: true, wantKept: "enroll"},
		{name: "kept browser", stored: "browser\n", want: false, wantKept: "browser\n"},
		{name: "kept value unknown", stored: "maybe", wantErr: true},
		{name: "first start, answer browser", ask: answer(joinBrowser), want: false, wantKept: "browser\n"},
		{name: "first start, answer enroll", ask: answer(joinEnroll), want: true, wantKept: "enroll\n"},
		{name: "first start without a terminal", want: true, wantKept: "enroll\n"},
		{name: "first start, asking fails", ask: func() (joinMode, error) { return "", errAsk }, wantErr: true},
		{name: "first start, keeping fails", ask: answer(joinBrowser), failWrite: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := &memFiles{files: map[string][]byte{}, failWrite: tt.failWrite}
			if tt.stored != "" {
				files.files[joinModeFile] = []byte(tt.stored)
			}
			cfg := config.Serve{ClientCertsSet: tt.flagSet, ClientCerts: tt.flagCerts}
			got, err := resolveClientCerts(cfg, files, tt.ask)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got != tt.want {
				t.Fatalf("client certificates = %v, want %v", got, tt.want)
			}
			if kept := string(files.files[joinModeFile]); kept != tt.wantKept {
				t.Fatalf("kept %q, want %q", kept, tt.wantKept)
			}
		})
	}
}

func TestAskJoinMode(t *testing.T) {
	tests := []struct {
		replies []string
		want    joinMode
		wantErr bool
	}{
		{[]string{""}, joinEnroll, false},
		{[]string{"1"}, joinEnroll, false},
		{[]string{" 2 "}, joinBrowser, false},
		{[]string{"x", "2"}, joinBrowser, false},
		{[]string{"x", "y", "z"}, "", true},
	}
	for _, tt := range tests {
		got, err := askJoinMode(&answers{replies: tt.replies})
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("%q: %q, %v; want %q, err %v", tt.replies, got, err, tt.want, tt.wantErr)
		}
	}
}
