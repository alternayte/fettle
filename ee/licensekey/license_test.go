//go:build ee

package licensekey_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/ee/licensekey"
)

func TestSCN_EE_002_LicenceKeysVerifyOffline(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	key := func(expires time.Time) string {
		k, err := licensekey.Sign(priv, licensekey.License{ID: "lic-1", Customer: "Acme", Seats: 25, Features: []string{licensekey.FeatureSSO, licensekey.FeatureAudit}, IssuedAt: now.AddDate(-1, 0, 0), Expires: expires})
		if err != nil {
			t.Fatal(err)
		}
		return k
	}

	valid := key(now.AddDate(0, 3, 0))
	st, err := licensekey.Verify(valid, pub, now)
	if err != nil || st.State != licensekey.Valid || st.License.Seats != 25 || !st.Has(licensekey.FeatureSSO, true) || st.Has(licensekey.FeatureSCIM, false) {
		t.Fatalf("valid key: %+v %v", st, err)
	}

	// Tampered: one changed byte in the body, or a key of another issuer.
	body, sig, _ := strings.Cut(valid, ".")
	tampered := body[:len(body)-2] + "x" + body[len(body)-1:] + "." + sig
	if st, err := licensekey.Verify(tampered, pub, now); err == nil || st.State != licensekey.Invalid || st.Has(licensekey.FeatureSSO, false) {
		t.Fatalf("tampered key: %+v %v", st, err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	foreign, _ := licensekey.Sign(other, licensekey.License{Customer: "Acme", Features: []string{licensekey.FeatureAllInOne}, Expires: now.AddDate(1, 0, 0)})
	if _, err := licensekey.Verify(foreign, pub, now); err == nil {
		t.Fatal("a key of another issuer verified")
	}

	// Expired within the grace: works, with a banner reason.
	st, err = licensekey.Verify(key(now.AddDate(0, 0, -5)), pub, now)
	if err != nil || st.State != licensekey.InGrace || st.Reason == "" || !st.Has(licensekey.FeatureSSO, true) || st.DaysLeft != 9 {
		t.Fatalf("grace key: %+v %v", st, err)
	}

	// Expired after the grace: the features read but do not change.
	st, err = licensekey.Verify(key(now.AddDate(0, 0, -20)), pub, now)
	if err != nil || st.State != licensekey.ReadOnly || !st.Has(licensekey.FeatureSSO, false) || st.Has(licensekey.FeatureSSO, true) {
		t.Fatalf("read-only key: %+v %v", st, err)
	}

	if st, _ := licensekey.Verify("", pub, now); st.State != licensekey.Missing || st.Has(licensekey.FeatureSSO, false) {
		t.Fatalf("no key: %+v", st)
	}
	if _, err := licensekey.Verify(valid, nil, now); err == nil {
		t.Fatal("a build without a public key accepted a key")
	}
}
