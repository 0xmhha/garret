package enroll

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
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

// Save writes the key, certificate and CA with mode 0600 in a 0700
// directory. Each file is written through a temporary file and a rename.
func (f Files) Save(key *ecdsa.PrivateKey, cert, ca *x509.Certificate) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return fmt.Errorf("enroll: create %s: %w", f.Dir, err)
	}
	if err := os.Chmod(f.Dir, 0o700); err != nil { //nolint:gosec // a directory needs the search bit; 0700 is owner-only
		return err
	}
	// SEC1 ("EC PRIVATE KEY") rather than PKCS#8: macOS security import
	// rejects a PKCS#8 PEM as an unknown format, and Git reads both.
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	for path, block := range map[string]*pem.Block{
		f.Key:  {Type: "EC PRIVATE KEY", Bytes: der},
		f.Cert: {Type: "CERTIFICATE", Bytes: cert.Raw},
		f.CA:   {Type: "CERTIFICATE", Bytes: ca.Raw},
	} {
		if err := writeFile(path, pem.EncodeToMemory(block)); err != nil {
			return err
		}
	}
	return nil
}

// Load reads the key and certificate back.
func (f Files) Load() (*ecdsa.PrivateKey, *x509.Certificate, error) {
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
