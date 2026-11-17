// Package web holds the web tools that run in the server: the search provider of a space,
// web_search and web_fetch.
package web

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/vault"
)

// Kinds of search providers.
const (
	KindTavily  = "tavily"
	KindExa     = "exa"
	KindSearXNG = "searxng"
	KindBrave   = "brave"
)

// Kinds lists the search provider kinds.
var Kinds = []string{KindTavily, KindExa, KindSearXNG, KindBrave}

// Names are the display names of the kinds.
var Names = map[string]string{KindTavily: "Tavily", KindExa: "Exa", KindSearXNG: "SearXNG", KindBrave: "Brave Search"}

// ErrNoProvider means the space has no search provider.
var ErrNoProvider = errors.New("this space has no web search provider")

// Service stores the search provider of each space. The key goes into the vault.
type Service struct {
	e     *drel.Engine
	vault *vault.Vault
	audit *audit.Log
}

// New returns the web service.
func New(e *drel.Engine, v *vault.Vault, a *audit.Log) *Service {
	return &Service{e: e, vault: v, audit: a}
}

// Input sets the search provider of a space. An empty Key keeps the stored key when the
// kind does not change.
type Input struct {
	Kind    string
	BaseURL string
	Key     string
}

// Get returns the search provider of a space, or ErrNoProvider.
func (s *Service) Get(ctx context.Context, spaceID uuid.UUID) (*SearchProvider, error) {
	p, err := drel.NewRepository(s.e, SearchProviderMeta).Where(SearchProviders.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNoProvider
	}
	return p, nil
}

// Set creates or replaces the search provider of a space.
func (s *Service) Set(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, in Input) (*SearchProvider, error) {
	in.Kind = strings.TrimSpace(in.Kind)
	in.Key = strings.TrimSpace(in.Key)
	if _, ok := Names[in.Kind]; !ok {
		return nil, fmt.Errorf("unknown search provider %q", in.Kind)
	}
	base := ""
	if in.Kind == KindSearXNG {
		base = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errors.New("the SearXNG URL must start with http:// or https://")
		}
	}
	var out *SearchProvider
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), SearchProviderMeta)
		p, err := repo.Where(SearchProviders.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		created := p == nil
		if created {
			p = &SearchProvider{SpaceID: spaceID}
		}
		sameKind := !created && p.Kind == in.Kind
		// A key belongs to one provider: a new kind drops the old key.
		if !sameKind && p.SecretItemID != nil && in.Key == "" {
			if err := s.vault.Delete(ctx, actor, spaceID, *p.SecretItemID); err != nil && !errors.Is(err, vault.ErrNotFound) {
				return err
			}
			p.SecretItemID = nil
		}
		if in.Key != "" {
			if p.SecretItemID != nil {
				if _, err := s.vault.Update(ctx, actor, spaceID, *p.SecretItemID, vault.Input{Value: in.Key}); err != nil {
					return err
				}
			} else {
				item, err := s.vault.Create(ctx, actor, spaceID, vault.Input{Name: "search key: " + Names[in.Kind], Kind: vault.KindSecret, Value: in.Key})
				if err != nil {
					return err
				}
				id := item.ID()
				p.SecretItemID = &id
			}
		}
		if in.Kind != KindSearXNG && p.SecretItemID == nil {
			return fmt.Errorf("enter the API key of %s", Names[in.Kind])
		}
		p.Kind, p.BaseURL = in.Kind, base
		if created {
			repo.Add(p)
		}
		out = p
		return s.audit.Record(ctx, actor, &spaceID, "web_search.set", "web_search_provider:"+p.ID().String(), map[string]string{"kind": p.Kind, "base_url": p.BaseURL})
	})
	return out, err
}

// Delete removes the search provider of a space and its key.
func (s *Service) Delete(ctx context.Context, actor audit.Actor, spaceID uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		p, err := s.Get(ctx, spaceID)
		if err != nil {
			return err
		}
		if _, err := drel.MustFromContext(ctx).Exec(ctx, "DELETE FROM web_search_providers WHERE id = $1", p.ID()); err != nil {
			return err
		}
		if p.SecretItemID != nil {
			if err := s.vault.Delete(ctx, actor, spaceID, *p.SecretItemID); err != nil && !errors.Is(err, vault.ErrNotFound) {
				return err
			}
		}
		return s.audit.Record(ctx, actor, &spaceID, "web_search.delete", "web_search_provider:"+p.ID().String(), map[string]string{"kind": p.Kind})
	})
}

// Conn returns the provider with its key from the vault. The key goes to the redactor.
func (s *Service) Conn(ctx context.Context, p *SearchProvider) (Provider, error) {
	c := Provider{Kind: p.Kind, BaseURL: p.BaseURL}
	if p.SecretItemID != nil {
		k, err := s.vault.Reveal(ctx, p.SpaceID, *p.SecretItemID)
		if err != nil {
			return Provider{}, fmt.Errorf("read the search key: %w", err)
		}
		c.Key = k
		s.vault.Redactor.Register(p.SpaceID, "search key "+Names[p.Kind], k)
	}
	return c, nil
}
