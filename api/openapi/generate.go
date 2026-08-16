package openapi

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -generate types,spec -package httpadapter -o ../../internal/adapters/http/openapi.gen.go talaria.yaml
