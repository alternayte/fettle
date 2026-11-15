//go:build ee

package main

// The Enterprise build registers its extension (§18.1). The default build never links ee.
import _ "github.com/alternayte/fettle/ee"
