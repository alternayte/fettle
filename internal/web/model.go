package web

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// SearchProvider is the web search provider of a space. A space has at most one.
type SearchProvider struct {
	drel.Model[uuid.UUID] `db:"table=web_search_providers,unique_index=uq_web_search_providers_space[space_id]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	Kind                  string    `db:"kind,check=kind IN ('tavily','exa','searxng','brave')"`
	// BaseURL is the address of a SearXNG server; the other kinds leave it empty.
	BaseURL      string     `db:"base_url"`
	SecretItemID *uuid.UUID `db:"secret_item_id,references=vault_items,on_delete=set_null"`
}
