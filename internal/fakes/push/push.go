// Package push is the push service mock of the tests (§24.4). It hands out subscriptions
// with real P-256 keys, records each Web Push delivery and decrypts it (RFC 8291).
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Subscription is what a browser would send to the server.
type Subscription struct {
	Endpoint string
	P256dh   string // base64url
	Auth     string // base64url
	key      *ecdh.PrivateKey
	auth     []byte
}

// Delivery is one decrypted push message.
type Delivery struct {
	Endpoint string
	TTL      string
	Urgency  string
	Payload  []byte
	Err      error
}

// Server records deliveries.
type Server struct {
	URL string // set by the caller after it starts an httptest server

	mu   sync.Mutex
	subs map[string]*Subscription
	got  []Delivery
}

// New returns a push mock.
func New() *Server { return &Server{subs: map[string]*Subscription{}} }

// Subscribe makes a subscription at this server.
func (s *Server) Subscribe(name string) (*Subscription, error) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		return nil, err
	}
	sub := &Subscription{Endpoint: strings.TrimRight(s.URL, "/") + "/push/" + name, P256dh: base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(auth), key: k, auth: auth}
	s.mu.Lock()
	s.subs["/push/"+name] = sub
	s.mu.Unlock()
	return sub, nil
}

// Deliveries returns the received messages.
func (s *Server) Deliveries() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Delivery(nil), s.got...)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sub := s.subs[r.URL.Path]
	s.mu.Unlock()
	if sub == nil || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	d := Delivery{Endpoint: sub.Endpoint, TTL: r.Header.Get("TTL"), Urgency: r.Header.Get("Urgency")}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "vapid ") {
		d.Err = errors.New("no VAPID authorization")
	} else {
		d.Payload, d.Err = decrypt(body, sub.key, sub.auth)
	}
	s.mu.Lock()
	s.got = append(s.got, d)
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

// decrypt opens an aes128gcm Web Push message (RFC 8188, RFC 8291).
func decrypt(body []byte, ua *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("short body")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	if len(body) < 21+idlen || rs < 18 {
		return nil, errors.New("bad header")
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	ct := body[21+idlen:]
	secret, err := ua.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	info := append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...)
	info = append(info, asPub.Bytes()...)
	ikm, err := hkdf.Key(sha256.New, secret, authSecret, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	i := len(plain) - 1
	for i >= 0 && plain[i] == 0 {
		i--
	}
	if i < 0 || plain[i] != 2 {
		return nil, errors.New("bad padding delimiter")
	}
	return plain[:i], nil
}
