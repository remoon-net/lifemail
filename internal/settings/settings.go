// Package settings keeps runtime configuration in the database so domains,
// TLS certificates and SMTP parameters can be changed without restarting the
// server.
package settings

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"sync"

	"remoon.net/lifemail/internal/store"
)

// Setting keys.
const (
	KeyDomains      = "mail.domains"
	KeyCatchAll     = "mail.catchall"
	KeySMTPHost     = "smtp.host"
	KeySMTPPort     = "smtp.port"
	KeySMTPUsername = "smtp.username"
	KeySMTPPassword = "smtp.password"
	KeySMTPFrom     = "smtp.from"
)

// Defaults used when a key has never been written.
const (
	DefaultSMTPHost     = "smtp.mx.cloudflare.net"
	DefaultSMTPPort     = 465
	DefaultSMTPUsername = "api_token"
)

// Domain is a mail domain.
//
// TLSCert and TLSKey are optional filesystem paths to a PEM certificate chain
// and private key. When configured, the server serves HTTPS for this domain
// (chosen by SNI at handshake time).
type Domain struct {
	Name    string `json:"name"`
	TLSCert string `json:"tls_cert,omitempty"`
	TLSKey  string `json:"tls_key,omitempty"`
}

// Config is an immutable snapshot of the runtime settings.
type Config struct {
	Domains []Domain
	// CatchAll maps a domain to the address that receives mail for unknown
	// recipients at that domain. Keys are lowercase domain names.
	CatchAll     map[string]string
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
}

// CatchAllFor returns the fallback destination configured for a domain.
func (c Config) CatchAllFor(domain string) (string, bool) {
	if len(c.CatchAll) == 0 {
		return "", false
	}
	dest, ok := c.CatchAll[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))]
	return dest, ok && dest != ""
}

// DomainNames returns just the configured domain names.
func (c Config) DomainNames() []string {
	names := make([]string, 0, len(c.Domains))
	for _, d := range c.Domains {
		names = append(names, d.Name)
	}
	return names
}

// Patch updates only the fields that are non-nil.
type Patch struct {
	Domains      *[]Domain
	CatchAll     *map[string]string
	SMTPHost     *string
	SMTPPort     *int
	SMTPUser     *string
	SMTPPassword *string
	SMTPFrom     *string
}

// Store caches settings and persists changes to the database.
type Store struct {
	db       *store.Store
	mu       sync.RWMutex
	cfg      Config
	certs    map[string]*tls.Certificate
	certErrs []string
}

// New loads settings, seeding defaults on first run.
func New(db *store.Store) (*Store, error) {
	s := &Store{db: db}
	if err := s.seedDefaults(); err != nil {
		return nil, err
	}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) seedDefaults() error {
	current, err := s.db.Settings()
	if err != nil {
		return err
	}
	defaults := map[string]string{
		KeySMTPHost:     DefaultSMTPHost,
		KeySMTPPort:     strconv.Itoa(DefaultSMTPPort),
		KeySMTPUsername: DefaultSMTPUsername,
	}
	missing := make(map[string]string)
	for key, value := range defaults {
		if _, ok := current[key]; !ok {
			missing[key] = value
		}
	}
	return s.db.SetSettings(missing)
}

// Reload refreshes the cached snapshot and TLS certificates from the database.
// Broken certificate files are reported through CertificateErrors instead of
// failing the reload.
func (s *Store) Reload() error {
	raw, err := s.db.Settings()
	if err != nil {
		return err
	}
	port := DefaultSMTPPort
	if v, err := strconv.Atoi(raw[KeySMTPPort]); err == nil && v > 0 {
		port = v
	}
	host := raw[KeySMTPHost]
	if host == "" {
		host = DefaultSMTPHost
	}
	username := raw[KeySMTPUsername]
	if username == "" {
		username = DefaultSMTPUsername
	}
	cfg := Config{
		Domains:      decodeDomains(raw[KeyDomains]),
		CatchAll:     decodeCatchAll(raw[KeyCatchAll]),
		SMTPHost:     host,
		SMTPPort:     port,
		SMTPUser:     username,
		SMTPPassword: raw[KeySMTPPassword],
		SMTPFrom:     raw[KeySMTPFrom],
	}
	certs, certErrs := loadCertificates(cfg.Domains)

	s.mu.Lock()
	s.cfg = cfg
	s.certs = certs
	s.certErrs = certErrs
	s.mu.Unlock()
	return nil
}

// Snapshot returns a copy of the current settings.
func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg
	cfg.Domains = append([]Domain(nil), s.cfg.Domains...)
	if s.cfg.CatchAll != nil {
		cfg.CatchAll = make(map[string]string, len(s.cfg.CatchAll))
		for domain, dest := range s.cfg.CatchAll {
			cfg.CatchAll[domain] = dest
		}
	}
	return cfg
}

// CertificateErrors reports certificates that could not be loaded.
func (s *Store) CertificateErrors() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.certErrs...)
}

// TLSConfig returns an SNI-aware TLS configuration, or nil when no domain has
// a usable certificate.
func (s *Store) TLSConfig() *tls.Config {
	s.mu.RLock()
	enabled := len(s.certs) > 0
	s.mu.RUnlock()
	if !enabled {
		return nil
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: s.GetCertificate,
	}
}

// GetCertificate selects the certificate matching the requested SNI name.
// Wildcard entries and a single configured certificate act as fallbacks.
func (s *Store) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	s.mu.RLock()
	defer s.mu.RUnlock()
	if cert, ok := s.certs[name]; ok {
		return cert, nil
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		if cert, ok := s.certs["*"+name[i:]]; ok {
			return cert, nil
		}
	}
	if len(s.certs) == 1 {
		for _, cert := range s.certs {
			return cert, nil
		}
	}
	return nil, fmt.Errorf("tls: no certificate configured for %q", hello.ServerName)
}

// Update validates and persists a patch, then refreshes the snapshot.
func (s *Store) Update(p Patch) (Config, error) {
	values := make(map[string]string)

	if p.Domains != nil {
		domains := NormalizeDomains(*p.Domains)
		if err := validateCertificates(domains); err != nil {
			return Config{}, err
		}
		values[KeyDomains] = encodeDomains(domains)
	}
	if p.CatchAll != nil {
		domains := s.Snapshot().Domains
		if p.Domains != nil {
			domains = NormalizeDomains(*p.Domains)
		}
		catchAll, err := normalizeCatchAll(*p.CatchAll, domains)
		if err != nil {
			return Config{}, err
		}
		values[KeyCatchAll] = encodeCatchAll(catchAll)
	}
	if p.SMTPHost != nil {
		host := strings.TrimSpace(*p.SMTPHost)
		if host == "" {
			return Config{}, errors.New("smtp host must not be empty")
		}
		if strings.Contains(host, "://") || strings.ContainsAny(host, " /") {
			return Config{}, errors.New("smtp host must be a bare hostname")
		}
		values[KeySMTPHost] = host
	}
	if p.SMTPPort != nil {
		if *p.SMTPPort < 1 || *p.SMTPPort > 65535 {
			return Config{}, fmt.Errorf("invalid smtp port %d", *p.SMTPPort)
		}
		values[KeySMTPPort] = strconv.Itoa(*p.SMTPPort)
	}
	if p.SMTPUser != nil {
		values[KeySMTPUsername] = strings.TrimSpace(*p.SMTPUser)
	}
	if p.SMTPPassword != nil {
		values[KeySMTPPassword] = *p.SMTPPassword
	}
	if p.SMTPFrom != nil {
		from := strings.TrimSpace(*p.SMTPFrom)
		if from != "" {
			if _, err := mail.ParseAddress(from); err != nil {
				return Config{}, fmt.Errorf("invalid smtp from address: %w", err)
			}
		}
		values[KeySMTPFrom] = from
	}

	if len(values) == 0 {
		return s.Snapshot(), nil
	}
	if err := s.db.SetSettings(values); err != nil {
		return Config{}, err
	}
	if err := s.Reload(); err != nil {
		return Config{}, err
	}
	return s.Snapshot(), nil
}

func loadCertificates(domains []Domain) (map[string]*tls.Certificate, []string) {
	certs := make(map[string]*tls.Certificate)
	var errs []string
	for _, domain := range domains {
		if domain.TLSCert == "" && domain.TLSKey == "" {
			continue
		}
		if domain.TLSCert == "" || domain.TLSKey == "" {
			errs = append(errs, domain.Name+": tls_cert and tls_key must both be set")
			continue
		}
		cert, err := tls.LoadX509KeyPair(domain.TLSCert, domain.TLSKey)
		if err != nil {
			errs = append(errs, domain.Name+": "+err.Error())
			continue
		}
		certs[strings.ToLower(domain.Name)] = &cert
	}
	return certs, errs
}

func validateCertificates(domains []Domain) error {
	for _, domain := range domains {
		if domain.TLSCert == "" && domain.TLSKey == "" {
			continue
		}
		if domain.TLSCert == "" || domain.TLSKey == "" {
			return fmt.Errorf("domain %s: tls_cert and tls_key must both be set", domain.Name)
		}
		if _, err := tls.LoadX509KeyPair(domain.TLSCert, domain.TLSKey); err != nil {
			return fmt.Errorf("domain %s: %w", domain.Name, err)
		}
	}
	return nil
}

// NormalizeDomains trims, lowercases and de-duplicates a domain list.
func NormalizeDomains(input []Domain) []Domain {
	var out []Domain
	seen := make(map[string]bool)
	for _, item := range input {
		parts := strings.FieldsFunc(item.Name, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
		})
		for _, part := range parts {
			name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(part), "@"))
			name = strings.TrimSuffix(name, ".")
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, Domain{
				Name:    name,
				TLSCert: strings.TrimSpace(item.TLSCert),
				TLSKey:  strings.TrimSpace(item.TLSKey),
			})
		}
	}
	return out
}

func encodeDomains(domains []Domain) string {
	if len(domains) == 0 {
		return ""
	}
	data, err := json.Marshal(domains)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeDomains(raw string) []Domain {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var list []Domain
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			return NormalizeDomains(list)
		}
	}
	// Backwards compatibility with the old comma separated list.
	return NormalizeDomains([]Domain{{Name: raw}})
}

// normalizeCatchAll validates per-domain fallback destinations. An empty
// destination removes the rule, which keeps "clear the input and save" working.
// Destinations on a configured domain are rejected: forwarding to ourselves
// would loop through the inbound endpoint forever.
func normalizeCatchAll(input map[string]string, domains []Domain) (map[string]string, error) {
	local := make(map[string]bool, len(domains))
	for _, d := range domains {
		local[strings.ToLower(d.Name)] = true
	}
	out := make(map[string]string, len(input))
	for domain, dest := range input {
		name := strings.ToLower(strings.TrimSuffix(
			strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(domain), "@")), "."))
		if name == "" {
			return nil, errors.New("catch-all domain must not be empty")
		}
		if len(local) > 0 && !local[name] {
			return nil, fmt.Errorf("catch-all domain %s is not configured", name)
		}
		dest = strings.TrimSpace(dest)
		if dest == "" {
			continue
		}
		addr, err := mail.ParseAddress(dest)
		if err != nil {
			return nil, fmt.Errorf("invalid catch-all destination for %s: %w", name, err)
		}
		target := strings.ToLower(addr.Address)
		at := strings.LastIndex(target, "@")
		if at < 1 || local[target[at+1:]] {
			return nil, fmt.Errorf("catch-all destination for %s must be an external address", name)
		}
		out[name] = target
	}
	return out, nil
}

func encodeCatchAll(rules map[string]string) string {
	if len(rules) == 0 {
		return ""
	}
	data, err := json.Marshal(rules)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeCatchAll(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var rules map[string]string
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil
	}
	out := make(map[string]string, len(rules))
	for domain, dest := range rules {
		name := strings.ToLower(strings.TrimSpace(domain))
		if name == "" || strings.TrimSpace(dest) == "" {
			continue
		}
		out[name] = strings.ToLower(strings.TrimSpace(dest))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
