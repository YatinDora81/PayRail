PROTO := proto/gateway/v1/gateway.proto

.PHONY: proto
proto: ## generate Go + gRPC stubs from the .proto
	protoc -I=proto \
		--go_out=. --go_opt=module=github.com/payrail/go \
		--go-grpc_out=. --go-grpc_opt=module=github.com/payrail/go \
		$(PROTO)

# The module= option strips the go_package import prefix
# (github.com/payrail/go) so the files land in internal/gatewaypb/ rather than a
# nested github.com/... tree. Without it, use --go_opt=paths=source_relative.

.PHONY: tidy
tidy: ## sync go.mod / go.sum
	go mod tidy

.PHONY: build
build: ## compile every binary
	go build ./...

.PHONY: vet
vet:
	go vet ./...