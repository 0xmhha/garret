package enroll

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Files are where one server's client credentials live on the device.
type Files struct {
	Dir  string
	Key  string
	Cert string
	CA   string
}

// FilesIn returns the file paths inside dir.
func FilesIn(dir string) Files {
	return Files{Dir: dir, Key: filepath.Join(dir, "key.pem"), Cert: filepath.Join(dir, "cert.pem"), CA: filepath.Join(dir, "ca.pem")}
}

// ClientDir is the per-server directory (transport.md 6.4): under
// ~/Library/Application Support/garret/client on macOS and
// $XDG_CONFIG_HOME/garret/client (default ~/.config) elsewhere.
func ClientDir(goos, home, xdgConfig, host string, port uint16) string {
	base := filepath.Join(home, ".config")
	if xdgConfig != "" {
		base = xdgConfig
	}
	if goos == "darwin" {
		base = filepath.Join(home, "Library", "Application Support")
	}
	return filepath.Join(base, "garret", "client", fmt.Sprintf("%s-%d", host, port))
}

// Suffixes of the directories Save swaps through.
const (
	stagingSuffix = ".new"
	retiredSuffix = ".old"
)

// Save replaces the key, certificate and CA together: it writes all three
// (mode 0600) into a staging directory (0700), then swaps that directory
// in with renames. A failure before the swap leaves the previous set in
// place; a crash between the two renames is finished by Load.
func (f Files) Save(key *ecdsa.PrivateKey, cert, ca *x509.Certificate) error {
	parent := filepath.Dir(f.Dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("enroll: create %s: %w", parent, err)
	}
	staging, retired := f.Dir+stagingSuffix, f.Dir+retiredSuffix
	if err := clearDir(staging); err != nil {
		return err
	}
	if err := os.Mkdir(staging, 0o700); err != nil {
		return fmt.Errorf("enroll: create %s: %w", staging, err)
	}
	// SEC1 ("EC PRIVATE KEY") rather than PKCS#8: macOS security import
	// rejects a PKCS#8 PEM as an unknown format, and Git reads both.
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	in := FilesIn(staging)
	for _, w := range []struct {
		path  string
		block *pem.Block
	}{
		{in.Key, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}},
		{in.Cert, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}},
		{in.CA, &pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}},
	} {
		if err := writeFile(w.path, pem.EncodeToMemory(w.block)); err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
	}
	if err := clearDir(retired); err != nil {
		return err
	}
	if err := os.Rename(f.Dir, retired); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("enroll: move the previous credentials aside: %w", err)
	}
	if err := os.Rename(staging, f.Dir); err != nil {
		return fmt.Errorf("enroll: install the new credentials: %w", err)
	}
	_ = os.RemoveAll(retired)
	return syncDir(parent)
}

// clearDir removes a leftover directory at path. Anything else there is an
// error, so Save never deletes a file it did not create.
func clearDir(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("enroll: %s is in the way; remove it", path)
	}
	return os.RemoveAll(path)
}

// recover finishes a swap that a crash interrupted: when the directory is
// missing, the complete staging directory, or else the previous one, takes
// its place.
func (f Files) recover() error {
	if _, err := os.Lstat(f.Dir); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	for _, src := range []string{f.Dir + stagingSuffix, f.Dir + retiredSuffix} {
		if fi, err := os.Lstat(src); err == nil && fi.IsDir() {
			return os.Rename(src, f.Dir)
		}
	}
	return nil
}

func syncDir(path string) error {
	d, err := os.Open(path) // #nosec G304 -- the client directory's parent
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// Load reads the key and certificate back.
func (f Files) Load() (*ecdsa.PrivateKey, *x509.Certificate, error) {
	if err := f.recover(); err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(f.Key)
	if err != nil {
		return nil, nil, err
	}
	certPEM, err := os.ReadFile(f.Cert)
	if err != nil {
		return nil, nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	cb, _ := pem.Decode(certPEM)
	if kb == nil || cb == nil {
		return nil, nil, errors.New("enroll: malformed key or certificate file")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("enroll: read %s: %w", f.Key, err)
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	return key, cert, err
}

// LoadCA reads the stored CA certificate.
func (f Files) LoadCA() (*x509.Certificate, error) {
	data, err := os.ReadFile(f.CA)
	if err != nil {
		return nil, err
	}
	return parsePEMCert(string(data))
}

func writeFile(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// GitConfigArgs returns the git commands that point Git at the files for
// serverURL only (http.<url>.sslCert and friends).
func GitConfigArgs(serverURL string, f Files) [][]string {
	prefix := "http." + serverURL + "/."
	return [][]string{
		{"config", "--global", prefix + "sslCert", f.Cert},
		{"config", "--global", prefix + "sslKey", f.Key},
		{"config", "--global", prefix + "sslCAInfo", f.CA},
	}
}
