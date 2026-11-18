package applecontainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// manifestTypes are the media types that the size lookup accepts.
var manifestTypes = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}

// imageRef is a registry image reference split into its parts.
type imageRef struct {
	registry, repo, ref string
}

func parseRef(image string) (imageRef, error) {
	host, rest, ok := strings.Cut(image, "/")
	if !ok || !strings.ContainsAny(host, ".:") {
		return imageRef{}, fmt.Errorf("the image %s names no registry", image)
	}
	r := imageRef{registry: host, repo: rest, ref: "latest"}
	if repo, digest, ok := strings.Cut(rest, "@"); ok {
		r.repo, r.ref = repo, digest
	} else if i := strings.LastIndex(rest, ":"); i > strings.LastIndex(rest, "/") {
		r.repo, r.ref = rest[:i], rest[i+1:]
	}
	return r, nil
}

type manifest struct {
	MediaType string `json:"mediaType"`
	Config    struct {
		Size int64 `json:"size"`
	} `json:"config"`
	Layers []struct {
		Size int64 `json:"size"`
	} `json:"layers"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
}

// imageSize returns the download size of the linux/arm64 image in bytes, from the registry
// manifest. It signs in without an account, as the CLI does for a public image.
func imageSize(ctx context.Context, hc *http.Client, image string) (int64, error) {
	r, err := parseRef(image)
	if err != nil {
		return 0, err
	}
	reg := &registry{hc: hc, base: "https://" + r.registry, repo: r.repo}
	m, err := reg.manifest(ctx, r.ref)
	if err != nil {
		return 0, err
	}
	if len(m.Manifests) > 0 {
		digest := ""
		for _, e := range m.Manifests {
			if e.Platform.OS == "linux" && e.Platform.Architecture == "arm64" {
				digest = e.Digest
				break
			}
		}
		if digest == "" {
			return 0, fmt.Errorf("the image %s has no linux/arm64 version", image)
		}
		if m, err = reg.manifest(ctx, digest); err != nil {
			return 0, err
		}
	}
	n := m.Config.Size
	for _, l := range m.Layers {
		n += l.Size
	}
	return n, nil
}

type registry struct {
	hc    *http.Client
	base  string
	repo  string
	token string
}

func (r *registry) manifest(ctx context.Context, ref string) (*manifest, error) {
	resp, err := r.get(ctx, r.base+"/v2/"+r.repo+"/manifests/"+ref)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized && r.token == "" {
		if err := r.signIn(ctx, resp.Header.Get("WWW-Authenticate")); err != nil {
			return nil, err
		}
		return r.manifest(ctx, ref)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the registry answers %s for %s:%s", resp.Status, r.repo, ref)
	}
	var m manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("read the manifest: %w", err)
	}
	return &m, nil
}

func (r *registry) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", strings.Join(manifestTypes, ", "))
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	return r.hc.Do(req)
}

// signIn gets an anonymous pull token from the realm of a Bearer challenge.
func (r *registry) signIn(ctx context.Context, challenge string) error {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return errors.New("the registry asks for a sign-in that Fettle does not know")
	}
	vals := map[string]string{}
	for _, part := range strings.Split(params, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			vals[strings.ToLower(k)] = strings.Trim(v, `"`)
		}
	}
	realm, err := url.Parse(vals["realm"])
	if err != nil || realm.Scheme != "https" {
		return errors.New("the registry gives no sign-in address")
	}
	q := realm.Query()
	if s := vals["service"]; s != "" {
		q.Set("service", s)
	}
	scope := vals["scope"]
	if scope == "" {
		scope = "repository:" + r.repo + ":pull"
	}
	q.Set("scope", scope)
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return err
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the registry refuses an anonymous pull of %s: %s", r.repo, resp.Status)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return err
	}
	r.token = tok.Token
	if r.token == "" {
		r.token = tok.AccessToken
	}
	if r.token == "" {
		return errors.New("the registry gave no pull token")
	}
	return nil
}
