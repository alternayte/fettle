package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Provider is a search provider with its key, ready to call.
type Provider struct {
	Kind    string
	BaseURL string
	Key     string
}

// Hit is one search result.
type Hit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Result is the answer of one search call.
type Result struct {
	Hits    []Hit
	CostUSD float64
}

// Endpoints are the API base URLs of the hosted providers. Tests point them at a fake.
var Endpoints = map[string]string{
	KindTavily: "https://api.tavily.com",
	KindExa:    "https://api.exa.ai",
	KindBrave:  "https://api.search.brave.com",
}

// Prices are the USD cost of one search call when the response does not state it, from
// the price pages of the providers on 2026-11-17. Exa states its cost in each response.
var Prices = map[string]float64{
	KindTavily:  0.008,
	KindExa:     0.005,
	KindBrave:   0.005,
	KindSearXNG: 0,
}

// MaxCount is the most results that one search returns.
const MaxCount = 20

// maxSearchBody is the most bytes of a search response that Fettle reads.
const maxSearchBody = 4 << 20

// Search runs one query and returns at most count results (1 to MaxCount, default 5).
func Search(ctx context.Context, hc *http.Client, p Provider, query string, count int) (Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, errors.New("the query is empty")
	}
	if count <= 0 {
		count = 5
	}
	count = min(count, MaxCount)
	var (
		req *http.Request
		err error
	)
	switch p.Kind {
	case KindTavily:
		req, err = jsonRequest(ctx, Endpoints[KindTavily]+"/search", map[string]any{"query": query, "max_results": count})
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+p.Key)
		}
	case KindExa:
		req, err = jsonRequest(ctx, Endpoints[KindExa]+"/search", map[string]any{"query": query, "numResults": count, "contents": map[string]any{"highlights": true}})
		if err == nil {
			req.Header.Set("x-api-key", p.Key)
		}
	case KindSearXNG:
		q := url.Values{"q": {query}, "format": {"json"}}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/")+"/search?"+q.Encode(), nil)
		if err == nil && p.Key != "" {
			req.Header.Set("Authorization", "Bearer "+p.Key)
		}
	case KindBrave:
		q := url.Values{"q": {query}, "count": {strconv.Itoa(count)}}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, Endpoints[KindBrave]+"/res/v1/web/search?"+q.Encode(), nil)
		if err == nil {
			req.Header.Set("X-Subscription-Token", p.Key)
		}
	default:
		return Result{}, fmt.Errorf("unknown search provider %q", p.Kind)
	}
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/json")
	name := Names[p.Kind]
	resp, err := hc.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("%s did not answer: %w", name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBody))
	if err != nil {
		return Result{}, fmt.Errorf("read the %s answer: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		if p.Kind == KindSearXNG && resp.StatusCode == http.StatusForbidden {
			return Result{}, errors.New("SearXNG refused the JSON format. Turn on the json format in SearXNG settings.yml")
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return Result{}, fmt.Errorf("%s refused the key (HTTP %d). Check the key in Settings, Web", name, resp.StatusCode)
		}
		return Result{}, fmt.Errorf("%s answered HTTP %d: %s", name, resp.StatusCode, snippet(body))
	}
	res, err := parse(p.Kind, body)
	if err != nil {
		return Result{}, fmt.Errorf("read the %s answer: %w", name, err)
	}
	if len(res.Hits) > count {
		res.Hits = res.Hits[:count]
	}
	return res, nil
}

func jsonRequest(ctx context.Context, u string, body any) (*http.Request, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func parse(kind string, body []byte) (Result, error) {
	res := Result{CostUSD: Prices[kind]}
	switch kind {
	case KindTavily, KindSearXNG:
		var v struct {
			Results []struct{ Title, URL, Content string } `json:"results"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			return res, err
		}
		for _, r := range v.Results {
			res.Hits = append(res.Hits, Hit{Title: r.Title, URL: r.URL, Snippet: r.Content})
		}
	case KindExa:
		var v struct {
			Results []struct {
				Title      string   `json:"title"`
				URL        string   `json:"url"`
				Text       string   `json:"text"`
				Highlights []string `json:"highlights"`
			} `json:"results"`
			CostDollars *struct {
				Total *float64 `json:"total"`
			} `json:"costDollars"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			return res, err
		}
		for _, r := range v.Results {
			s := strings.Join(r.Highlights, " … ")
			if s == "" {
				s = r.Text
			}
			res.Hits = append(res.Hits, Hit{Title: r.Title, URL: r.URL, Snippet: s})
		}
		if v.CostDollars != nil && v.CostDollars.Total != nil {
			res.CostUSD = *v.CostDollars.Total
		}
	case KindBrave:
		var v struct {
			Web struct {
				Results []struct{ Title, URL, Description string } `json:"results"`
			} `json:"web"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			return res, err
		}
		for _, r := range v.Web.Results {
			res.Hits = append(res.Hits, Hit{Title: r.Title, URL: r.URL, Snippet: r.Description})
		}
	}
	for i := range res.Hits {
		res.Hits[i].Snippet = cutRunes(strings.TrimSpace(res.Hits[i].Snippet), 1000)
	}
	return res, nil
}

// Format writes the results as a numbered list for the model.
func Format(query string, hits []Hit) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No results for %q.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Results for %q. Cite the URL of each fact that you use.\n", query)
	for i, h := range hits {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, strings.TrimSpace(h.Title), h.URL)
		if h.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", strings.ReplaceAll(h.Snippet, "\n", " "))
		}
	}
	return b.String()
}

func snippet(b []byte) string {
	return cutRunes(strings.TrimSpace(string(b)), 200)
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
