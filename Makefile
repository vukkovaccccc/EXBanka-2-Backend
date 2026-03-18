.PHONY: proto tidy build-user build-notif build-bank build-all docker-up docker-down \
        test test-coverage test-coverage-filtered generate-mocks

# ─── Proto generation ────────────────────────────────────────────────────────
# Requires: protoc, protoc-gen-go, protoc-gen-go-grpc
# Install:  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#           go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
proto:
	protoc \
		-I . -I third_party/googleapis \
		--go_out=. --go_opt=module=banka-backend \
		--go-grpc_out=. --go-grpc_opt=module=banka-backend \
		--grpc-gateway_out=. --grpc-gateway_opt=module=banka-backend \
		proto/user/user.proto
	protoc \
		-I . -I third_party/googleapis \
		--go_out=. --go_opt=module=banka-backend \
		--go-grpc_out=. --go-grpc_opt=module=banka-backend \
		proto/notification/notification.proto
	protoc \
		-I . -I third_party/googleapis \
		--go_out=. --go_opt=module=banka-backend \
		--go-grpc_out=. --go-grpc_opt=module=banka-backend \
		--grpc-gateway_out=. --grpc-gateway_opt=module=banka-backend \
		proto/banka/banka.proto

# ─── Go module ───────────────────────────────────────────────────────────────
tidy:
	go mod tidy

# ─── Testing ───────────────────────────────────────────────────────────────

# Run all unit tests
test:
	go test ./services/... -v -count=1

# Run tests with coverage (filtered) and generate HTML report
coverage:
	bash scripts/coverage.sh

# Regenerate mocks
generate-mocks:
	mockery --config .mockery.yaml

# ─── Local builds ────────────────────────────────────────────────────────────
build-user:
	CGO_ENABLED=0 go build -o bin/user-service ./services/user-service/cmd/server

build-notif:
	CGO_ENABLED=0 go build -o bin/notification-service ./services/notification-service/cmd/server

build-bank:
	CGO_ENABLED=0 go build -o bin/bank-service ./services/bank-service/cmd/server

build-all: build-user build-notif build-bank

# ─── Docker ──────────────────────────────────────────────────────────────────
docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v

logs-user:
	docker compose logs -f user-service

logs-notif:
	docker compose logs -f notification-service

logs-bank:
	docker compose logs -f bank-service
