//go:build ee

// Package license verifies Fettle Enterprise licence keys offline (§18.3): an Ed25519
// signature over a JSON body, checked with the public key in the binary. There is no
// phone-home. After expiry the key works for 14 days of grace, then the ee features are
// read-only.
package licensekey

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// PublicKey is the base64 Ed25519 public key of the licence issuer, set by the release
// build (-X github.com/alternayte/fettle/ee/licensekey.PublicKey=...).
var PublicKey = ""

// Grace is how long a key works after it expires.
const Grace = 14 * 24 * time.Hour

// Features of the ee build.
const (
	FeatureSSO      = "sso"
	FeatureSCIM     = "scim"
	FeatureAudit    = "audit_export"
	FeaturePolicy   = "policy_packs"
	FeatureBudgets  = "org_budgets"
	FeatureConsole  = "admin_console"
	FeatureAllInOne = "all"
)

// License is the body of a key.
type License struct {
	ID       string    `json:"id"`
	Customer string    `json:"customer"`
	Seats    int       `json:"seats"`
	Features []string  `json:"features"`
	IssuedAt time.Time `json:"issued_at"`
	Expires  time.Time `json:"expires"`
}

// State of a key.
type State string

// States.
const (
	Missing  State = "missing"
	Invalid  State = "invalid"
	Valid    State = "valid"
	InGrace  State = "grace"
	ReadOnly State = "read_only"
)

// Status is the result of a verification.
type Status struct {
	State   State    `json:"state"`
	License *License `json:"license,omitempty"`
	// DaysLeft is the days until expiry (valid) or until read-only (grace).
	DaysLeft int    `json:"days_left"`
	Reason   string `json:"reason,omitempty"`
}

// Errors of verification.
var (
	ErrFormat    = errors.New("the licence key is not in the Fettle format")
	ErrSignature = errors.New("the licence key is not signed by Fettle")
	ErrNoKey     = errors.New("this build has no licence public key")
)

// Verify checks a key "<base64url body>.<base64url signature>" with pub at now.
func Verify(key string, pub ed25519.PublicKey, now time.Time) (Status, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Status{State: Missing}, nil
	}
	if len(pub) != ed25519.PublicKeySize {
		return Status{State: Invalid, Reason: ErrNoKey.Error()}, ErrNoKey
	}
	body, sig, ok := strings.Cut(key, ".")
	if !ok {
		return Status{State: Invalid, Reason: ErrFormat.Error()}, ErrFormat
	}
	raw, err1 := base64.RawURLEncoding.DecodeString(body)
	s, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil {
		return Status{State: Invalid, Reason: ErrFormat.Error()}, ErrFormat
	}
	if !ed25519.Verify(pub, raw, s) {
		return Status{State: Invalid, Reason: ErrSignature.Error()}, ErrSignature
	}
	var l License
	if err := json.Unmarshal(raw, &l); err != nil {
		return Status{State: Invalid, Reason: ErrFormat.Error()}, ErrFormat
	}
	st := Status{License: &l}
	switch {
	case now.Before(l.Expires):
		st.State, st.DaysLeft = Valid, int(l.Expires.Sub(now).Hours()/24)
	case now.Before(l.Expires.Add(Grace)):
		st.State, st.DaysLeft = InGrace, int(l.Expires.Add(Grace).Sub(now).Hours()/24)
		st.Reason = "The licence expired. Enterprise features stop changing data when the grace ends."
	default:
		st.State = ReadOnly
		st.Reason = "The licence expired more than 14 days ago. Enterprise features are read-only."
	}
	return st, nil
}

// Sign makes a key (the issuer's tool and tests).
func Sign(priv ed25519.PrivateKey, l License) (string, error) {
	raw, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, raw)), nil
}

// Has reports whether a status lets a feature work; write is true for a change.
func (s Status) Has(feature string, write bool) bool {
	if s.License == nil || (s.State != Valid && s.State != InGrace && s.State != ReadOnly) {
		return false
	}
	if write && s.State == ReadOnly {
		return false
	}
	return slices.Contains(s.License.Features, feature) || slices.Contains(s.License.Features, FeatureAllInOne)
}
