// Package api embeds the OpenAPI document so the service can serve it and
// tests can verify the implementation against it.
package api

import _ "embed"

// Spec is the OpenAPI 3.1 document describing the HTTP API.
//
//go:embed openapi.yaml
var Spec []byte
