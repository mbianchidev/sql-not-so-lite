package etcqlite

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/config"
)

var (
	ErrConsent           = errors.New("include_data and ack_sensitive_data must both be true or both be false")
	ErrIdentityChanged   = errors.New("Etcqlite identity changed; remove the old inspection and explicitly reconnect")
	ErrInvalidProjection = errors.New("invalid or unsafe Etcqlite inspection projection")
)

const maxProjectionBytes = 64 * 1024 * 1024

type Discovery struct {
	Format                int      `json:"format"`
	Kind                  string   `json:"kind"`
	ClusterID             string   `json:"cluster_id"`
	MemberID              string   `json:"member_id"`
	Revision              int64    `json:"revision"`
	SchemaVersion         uint32   `json:"schema_version"`
	EngineVersion         string   `json:"engine_version"`
	APIVersion            string   `json:"api_version"`
	Build                 string   `json:"build"`
	Dirty                 bool     `json:"dirty"`
	ConfigurationID       *uint32  `json:"configuration_id"`
	Capabilities          []string `json:"capabilities"`
	SnapshotPath          string   `json:"snapshot_path"`
	ProjectionPath        string   `json:"projection_path"`
	Inspection            string   `json:"inspection"`
	RecoverableProjection bool     `json:"recoverable_projection"`
	ETag                  string   `json:"-"`
}

type Metadata struct {
	ClusterID       string
	MemberID        string
	Revision        int64
	CompactRevision int64
	SourceSchema    uint32
	IncludesData    bool
}

type Projection struct {
	Path     string
	Size     int64
	Metadata Metadata
}

type HTTPStatusError struct {
	Status    int
	Operation string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("Etcqlite %s returned HTTP %d (%s)", e.Operation, e.Status, http.StatusText(e.Status))
}

func (e *HTTPStatusError) Is(target error) bool {
	return target == ErrIdentityChanged && e.Status == http.StatusPreconditionFailed
}

type Client struct {
	endpoint  *url.URL
	token     string
	http      *http.Client
	transport *http.Transport
}

func NewClient(cfg config.EtcqliteConfig) (*Client, error) {
	if err := cfg.ValidateTransport(); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	token := cfg.AuthToken
	if token == "" && cfg.AuthTokenFile != "" {
		data, err := readPrivateCredential(cfg.AuthTokenFile)
		if err != nil {
			return nil, fmt.Errorf("read Etcqlite token file: %w", err)
		}
		token = strings.TrimSpace(string(data))
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return nil, fmt.Errorf("Etcqlite token file must contain one nonempty token")
		}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSCAFile != "" {
		data, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read Etcqlite CA certificate: %w", err)
		}
		tlsConfig.RootCAs = x509.NewCertPool()
		if !tlsConfig.RootCAs.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("Etcqlite CA file contains no valid certificates")
		}
	}
	if cfg.TLSCertFile != "" {
		certificate, err := os.ReadFile(cfg.TLSCertFile)
		if err != nil {
			return nil, fmt.Errorf("read Etcqlite client certificate: %w", err)
		}
		key, err := readPrivateCredential(cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read Etcqlite client key: %w", err)
		}
		pair, err := tls.X509KeyPair(certificate, key)
		if err != nil {
			return nil, fmt.Errorf("invalid Etcqlite TLS client certificate/key pair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil, TLSClientConfig: tlsConfig,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		peer, ok := connection.RemoteAddr().(*net.TCPAddr)
		if !ok || (!peer.IP.IsLoopback() && (endpoint.Scheme != "https" || len(tlsConfig.Certificates) == 0)) {
			connection.Close()
			return nil, fmt.Errorf("non-loopback Etcqlite traffic requires mTLS")
		}
		return connection, nil
	}
	return &Client{
		endpoint: endpoint, token: token, transport: transport,
		http: &http.Client{
			Transport: transport, Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func readPrivateCredential(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("credential must be a regular file accessible only to its owner (chmod 600)")
	}
	return os.ReadFile(path)
}

func (c *Client) Close() { c.transport.CloseIdleConnections() }

func (c *Client) request(ctx context.Context, method, path string, body []byte, etag string) (*http.Response, error) {
	target := *c.endpoint
	target.Path = path
	target.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		request.Header.Set("Authorization", c.token)
	}
	request.Header.Set("Accept", "application/json, application/vnd.sqlite3, application/octet-stream")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("If-Match", etag)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("contact Etcqlite inspector: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, &HTTPStatusError{Status: response.StatusCode, Operation: strings.TrimPrefix(path, "/etcqlite/v1/")}
	}
	return response, nil
}

func (c *Client) Discover(ctx context.Context) (*Discovery, error) {
	response, err := c.request(ctx, http.MethodGet, "/etcqlite/v1/discovery", nil, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var discovery Discovery
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, fmt.Errorf("Etcqlite discovery exceeds the 64 KiB limit or could not be read")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&discovery); err != nil {
		return nil, fmt.Errorf("invalid Etcqlite discovery response")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("Etcqlite discovery must contain one bounded JSON object")
	}
	clusterID, err := decimalID(discovery.ClusterID)
	if err != nil {
		return nil, err
	}
	memberID, err := decimalID(discovery.MemberID)
	if err != nil {
		return nil, err
	}
	discovery.ETag = response.Header.Get("ETag")
	expectedETag := fmt.Sprintf(`"%016x:%016x"`, clusterID, memberID)
	if discovery.Format != 1 || discovery.Kind != "etcqlite" || discovery.Revision < 0 ||
		discovery.SchemaVersion == 0 || discovery.ETag != expectedETag ||
		discovery.EngineVersion == "" || discovery.APIVersion == "" || discovery.Build == "" ||
		(discovery.ConfigurationID != nil && *discovery.ConfigurationID == 0) ||
		discovery.ProjectionPath != "/etcqlite/v1/projections" ||
		!slices.Contains(discovery.Capabilities, "read-only-projection-v1") ||
		discovery.RecoverableProjection {
		return nil, fmt.Errorf("unsupported or inconsistent Etcqlite discovery contract")
	}
	return &discovery, nil
}

func decimalID(value string) (uint64, error) {
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(id, 10) != value {
		return 0, fmt.Errorf("Etcqlite identities must be canonical decimal uint64 strings")
	}
	return id, nil
}

func (c *Client) Projection(ctx context.Context, discovery *Discovery, includeData, ackSensitive bool, dir string) (*Projection, error) {
	if includeData != ackSensitive {
		return nil, ErrConsent
	}
	body := []byte("{}")
	if includeData {
		body = []byte(`{"include_data":true,"ack_sensitive_data":true}`)
	}
	response, err := c.request(ctx, http.MethodPost, "/etcqlite/v1/projections", body, discovery.ETag)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > maxProjectionBytes {
		return nil, fmt.Errorf("Etcqlite projection exceeds the 64 MiB inspection limit")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create private inspection directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("inspection directory must be a private directory, not a symlink (chmod 700)")
	}
	file, err := os.CreateTemp(dir, "etcqlite-projection-*.sqlite")
	if err != nil {
		return nil, fmt.Errorf("create private projection: %w", err)
	}
	path := file.Name()
	keep := false
	defer func() {
		if !keep {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("remove failed Etcqlite projection: %v", err)
			}
		}
	}()
	size, copyErr := io.Copy(file, io.LimitReader(response.Body, maxProjectionBytes+1))
	if copyErr == nil && size > maxProjectionBytes {
		copyErr = fmt.Errorf("Etcqlite projection exceeds the 64 MiB inspection limit")
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return nil, fmt.Errorf("save Etcqlite projection: %w", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		return nil, fmt.Errorf("protect Etcqlite projection: %w", err)
	}
	db, err := openImmutable(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open artifact", ErrInvalidProjection)
	}
	metadata, validationErr := validateProjection(ctx, db, discovery, includeData)
	closeErr = db.Close()
	if err := errors.Join(validationErr, closeErr); err != nil {
		return nil, err
	}
	keep = true
	return &Projection{Path: path, Size: size, Metadata: metadata}, nil
}

func ProjectionDir(dataDir string) string { return filepath.Join(dataDir, "inspections") }

func OwnsProjection(dataDir, path string) bool {
	return filepath.Clean(filepath.Dir(path)) == filepath.Clean(ProjectionDir(dataDir)) &&
		strings.HasPrefix(filepath.Base(path), "etcqlite-projection-") &&
		strings.HasSuffix(path, ".sqlite")
}
