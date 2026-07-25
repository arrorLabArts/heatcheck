package apidocs

import _ "embed"

// Spec is the public OpenAPI 3.1 contract.
//
//go:embed openapi.yaml
var Spec []byte
