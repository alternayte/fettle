package app

import (
	"context"
	"net/http"
	"time"

	"github.com/alternayte/fettle/internal/vault"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// VaultItemView is a vault item without its value (REQ-VLT-002).
type VaultItemView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Site      string    `json:"site"`
	Username  string    `json:"username"`
	Last4     string    `json:"last4"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func itemView(it *vault.VaultItem) VaultItemView {
	return VaultItemView{ID: it.ID().String(), Name: it.Name, Kind: it.Kind, Site: it.Site, Username: it.Username, Last4: it.Last4, CreatedAt: it.CreatedAt(), UpdatedAt: it.UpdatedAt()}
}

type vaultBody struct {
	Name     string `json:"name,omitempty" maxLength:"120"`
	Kind     string `json:"kind,omitempty" enum:"login,secret,totp"`
	Site     string `json:"site,omitempty" maxLength:"500"`
	Username string `json:"username,omitempty" maxLength:"200"`
	Value    string `json:"value,omitempty" maxLength:"20000" doc:"Write only. The API never returns it."`
}

type ItemPath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Item  uuid.UUID `path:"item"`
}

func (a *App) registerVault(api huma.API) {
	huma.Register(api, op("list-vault-items", http.MethodGet, "/spaces/{space}/vault", "List vault items without values", "vault"), func(ctx context.Context, in *SpacePath) (*struct {
		Body struct {
			Items []VaultItemView `json:"items"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		items, err := a.Vault.List(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Items []VaultItemView `json:"items"`
			}
		}{}
		out.Body.Items = []VaultItemView{}
		for _, it := range items {
			out.Body.Items = append(out.Body.Items, itemView(it))
		}
		return out, nil
	})

	huma.Register(api, op("create-vault-item", http.MethodPost, "/spaces/{space}/vault", "Store a login, a secret or a TOTP seed", "vault"), func(ctx context.Context, in *struct {
		SpacePath
		Body vaultBody
	}) (*struct{ Body VaultItemView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		it, err := a.Vault.Create(ctx, actorOf(p), in.Space, vault.Input{Name: in.Body.Name, Kind: in.Body.Kind, Site: in.Body.Site, Username: in.Body.Username, Value: in.Body.Value})
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return &struct{ Body VaultItemView }{Body: itemView(it)}, nil
	})

	huma.Register(api, op("update-vault-item", http.MethodPatch, "/spaces/{space}/vault/{item}", "Change a vault item. An empty value keeps the stored value.", "vault"), func(ctx context.Context, in *struct {
		ItemPath
		Body vaultBody
	}) (*struct{ Body VaultItemView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		it, err := a.Vault.Update(ctx, actorOf(p), in.Space, in.Item, vault.Input{Name: in.Body.Name, Site: in.Body.Site, Username: in.Body.Username, Value: in.Body.Value})
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body VaultItemView }{Body: itemView(it)}, nil
	})

	huma.Register(api, op("delete-vault-item", http.MethodDelete, "/spaces/{space}/vault/{item}", "Delete a vault item", "vault"), func(ctx context.Context, in *ItemPath) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, apiError(a.Vault.Delete(ctx, actorOf(p), in.Space, in.Item))
	})
}
