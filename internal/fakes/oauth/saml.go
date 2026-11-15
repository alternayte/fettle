package oauth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/crewjam/saml"
)

// SAML is a fake SAML 2.0 identity provider for tests. It signs in the current person at
// once and answers with a signed assertion that carries the attribute "groups". It reads the
// service provider metadata from SPMetadataURL.
type SAML struct {
	*httptest.Server
	// SPMetadataURL is where the fake reads the metadata of the service provider.
	SPMetadataURL string

	idp *saml.IdentityProvider
	mu  sync.Mutex
	who Person
}

// NewSAML starts a fake identity provider. Close it after the test.
func NewSAML() *SAML {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake-idp.acme.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, _ := x509.ParseCertificate(der)
	s := &SAML{}
	s.Server = httptest.NewUnstartedServer(nil)
	s.Start()
	base, _ := url.Parse(s.URL)
	s.idp = &saml.IdentityProvider{
		Key: key, Certificate: cert, Logger: discard{},
		MetadataURL:             *base.ResolveReference(&url.URL{Path: "/metadata"}),
		SSOURL:                  *base.ResolveReference(&url.URL{Path: "/sso"}),
		SessionProvider:         s,
		ServiceProviderProvider: s,
	}
	s.Config.Handler = s.idp.Handler()
	return s
}

// SignInAs sets the person that the next request signs in.
func (s *SAML) SignInAs(p Person) {
	s.mu.Lock()
	s.who = p
	s.mu.Unlock()
}

// MetadataURL is the address of the identity provider metadata.
func (s *SAML) MetadataURL() string { return s.URL + "/metadata" }

// GetSession implements saml.SessionProvider.
func (s *SAML) GetSession(_ http.ResponseWriter, _ *http.Request, _ *saml.IdpAuthnRequest) *saml.Session {
	s.mu.Lock()
	p := s.who
	s.mu.Unlock()
	values := make([]saml.AttributeValue, 0, len(p.Groups))
	for _, g := range p.Groups {
		values = append(values, saml.AttributeValue{Type: "xs:string", Value: g})
	}
	return &saml.Session{
		ID: random(), CreateTime: time.Now(), ExpireTime: time.Now().Add(time.Hour), Index: random(),
		NameID: p.Email, NameIDFormat: "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
		UserEmail: p.Email, UserCommonName: p.Name,
		CustomAttributes: []saml.Attribute{{Name: "groups", NameFormat: "urn:oasis:names:tc:SAML:2.0:attrname-format:basic", Values: values}},
	}
}

// GetServiceProvider implements saml.ServiceProviderProvider.
func (s *SAML) GetServiceProvider(_ *http.Request, _ string) (*saml.EntityDescriptor, error) {
	resp, err := http.Get(s.SPMetadataURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, os.ErrNotExist
	}
	var ed saml.EntityDescriptor
	if err := xml.NewDecoder(bytes.NewReader(b)).Decode(&ed); err != nil {
		return nil, fmt.Errorf("service provider metadata: %w", err)
	}
	return &ed, nil
}

type discard struct{}

func (discard) Printf(string, ...interface{}) {}
func (discard) Print(...interface{})          {}
func (discard) Println(...interface{})        {}
func (discard) Fatal(...interface{})          {}
func (discard) Fatalf(string, ...interface{}) {}
func (discard) Fatalln(...interface{})        {}
func (discard) Panic(...interface{})          {}
func (discard) Panicf(string, ...interface{}) {}
func (discard) Panicln(...interface{})        {}
