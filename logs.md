## gofmt

```
Run unformatted="$(gofmt -l .)"
  unformatted="$(gofmt -l .)"
  if [ -n "$unformatted" ]; then
    echo "The following files are not gofmt-formatted:" >&2
    echo "$unformatted" >&2
    exit 1
  fi
  shell: /usr/bin/bash -e {0}
  env:
    GOTOOLCHAIN: local
The following files are not gofmt-formatted:
services/bank-service/cmd/server/main.go
services/bank-service/internal/config/config.go
services/bank-service/internal/domain/account.go
services/bank-service/internal/domain/berza.go
services/bank-service/internal/domain/exchange.go
services/bank-service/internal/domain/kartica.go
services/bank-service/internal/domain/kredit.go
services/bank-service/internal/domain/payment.go
services/bank-service/internal/handler/actuary_handler.go
services/bank-service/internal/handler/berza_handler.go
services/bank-service/internal/handler/exchange_handler.go
services/bank-service/internal/handler/fund_handler.go
services/bank-service/internal/handler/grpc_handler.go
services/bank-service/internal/handler/internal_actuary_handler.go
services/bank-service/internal/handler/kartica_handler.go
services/bank-service/internal/handler/klient_kartice_handler.go
services/bank-service/internal/handler/kredit_handler.go
services/bank-service/internal/handler/listing_handler.go
services/bank-service/internal/handler/listing_order_handler.go
services/bank-service/internal/handler/market_mode_http_handler.go
services/bank-service/internal/handler/my_orders_handler.go
services/bank-service/internal/handler/payment_handler.go
services/bank-service/internal/handler/pdf_handler.go
services/bank-service/internal/handler/portfolio_handler.go
services/bank-service/internal/handler/tax_handler.go
services/bank-service/internal/handler/trading_fx_handler.go
services/bank-service/internal/handler/trading_handler.go
services/bank-service/internal/repository/account_repository.go
services/bank-service/internal/repository/actuary_repository.go
services/bank-service/internal/repository/funds_manager.go
services/bank-service/internal/repository/kredit_repository.go
services/bank-service/internal/repository/order_repository.go
services/bank-service/internal/repository/payment_repository.go
services/bank-service/internal/service/berza_service.go
services/bank-service/internal/service/exchange_service.go
services/bank-service/internal/service/kartica_service.go
services/bank-service/internal/service/kredit_service.go
services/bank-service/internal/service/kredit_service_test.go
services/bank-service/internal/service/tax_service.go
services/bank-service/internal/trading/domain.go
services/bank-service/internal/trading/service.go
services/bank-service/internal/worker/account_notification.go
services/bank-service/internal/worker/listing_refresher.go
services/bank-service/internal/worker/yahoo_chart.go
services/bank-service/tests/bdd/krediti_steps_test.go
services/notification-service/internal/service/notification_service.go
services/notification-service/internal/service/notification_service_test.go
services/user-service/cmd/server/main.go
services/user-service/internal/handler/grpc_handler.go
services/user-service/internal/handler/grpc_handler_test.go
services/user-service/internal/interceptor/auth_interceptor.go
services/user-service/internal/repository/user_repository.go
services/user-service/internal/service/user_service.go
Error: Process completed with exit code 1.
```

## go mod tidy

```
Run git diff --exit-code -- go.mod go.sum
  git diff --exit-code -- go.mod go.sum
  shell: /usr/bin/bash -e {0}
  env:
    GOTOOLCHAIN: local
diff --git a/go.mod b/go.mod
index 84be5e6..d000e9a 100755
--- a/go.mod
+++ b/go.mod
@@ -15,6 +15,7 @@ require (
 	github.com/joho/godotenv v1.5.1
 	github.com/rabbitmq/amqp091-go v1.10.0
 	github.com/redis/go-redis/v9 v9.18.0
+	github.com/shopspring/decimal v1.4.0
 	github.com/stretchr/testify v1.10.0
 	golang.org/x/crypto v0.46.0
 	google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57
@@ -59,7 +60,6 @@ require (
 	github.com/pelletier/go-toml/v2 v2.2.2 // indirect
 	github.com/pmezard/go-difflib v1.0.0 // indirect
 	github.com/rogpeppe/go-internal v1.14.1 // indirect
-	github.com/shopspring/decimal v1.4.0 // indirect
 	github.com/spf13/pflag v1.0.7 // indirect
 	github.com/stretchr/objx v0.5.2 // indirect
 	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
Error: Process completed with exit code 1.

```

## golangci-lint

```
Run golangci/golangci-lint-action@v9
  with:
    version: v2.11
    args: --timeout=5m
    install-mode: binary
    install-only: false
    github-token: ***
    verify: true
    only-new-issues: false
    skip-cache: false
    skip-save-cache: false
    cache-invalidation-interval: 7
    problem-matchers: false
  env:
    GOTOOLCHAIN: local
Restore cache
  Checking for go.mod: go.mod
  (node:2256) [DEP0040] DeprecationWarning: The `punycode` module is deprecated. Please use a userland alternative instead.
  (Use `node --trace-deprecation ...` to show where the warning was created)
  Cache not found for input keys: golangci-lint.cache-Linux-2937-eafdb0672213e1915a64d045878d50a496fdac8b, golangci-lint.cache-Linux-2937-
Install
  Finding needed golangci-lint version...
  Requested golangci-lint 'v2.11', using 'v2.11.4', calculation took 26ms
  Installation mode: binary
  Installing golangci-lint binary v2.11.4...
  Downloading binary https://github.com/golangci/golangci-lint/releases/download/v2.11.4/golangci-lint-2.11.4-linux-amd64.tar.gz ...
  /usr/bin/tar xz --overwrite --warning=no-unknown-keyword --overwrite -C /home/runner -f /home/runner/work/_temp/f16488f5-8e56-43b5-9859-91b7bc611f0e
  Installed golangci-lint into /home/runner/golangci-lint-2.11.4-linux-amd64/golangci-lint in 3768ms
run golangci-lint
  Running [/home/runner/golangci-lint-2.11.4-linux-amd64/golangci-lint config path] in [/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend] ...
  Running [/home/runner/golangci-lint-2.11.4-linux-amd64/golangci-lint config verify] in [/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend] ...
  Running [/home/runner/golangci-lint-2.11.4-linux-amd64/golangci-lint run  --timeout=5m] in [/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend] ...
  Error: services/bank-service/cmd/server/main.go:67:19: Error return value of `sqlDB.Close` is not checked (errcheck)
  	defer sqlDB.Close()
  	                 ^
  Error: services/bank-service/cmd/server/main.go:122:17: Error return value of `nc.Close` is not checked (errcheck)
  		defer nc.Close()
  		              ^
  Error: services/bank-service/cmd/server/main.go:160:24: Error return value of `userClient.Close` is not checked (errcheck)
  	defer userClient.Close()
  	                      ^
  Error: services/bank-service/internal/handler/my_orders_handler.go:142:27: Error return value of `(*encoding/json.Encoder).Encode` is not checked (errcheck)
  	json.NewEncoder(w).Encode(map[string]interface{}{"orders": result})
  	                         ^
  Error: services/bank-service/internal/handler/pdf_handler.go:96:12: Error return value of `fmt.Fprint` is not checked (errcheck)
  	fmt.Fprint(w, html)
  	          ^
  Error: services/bank-service/internal/handler/trading_fx_handler.go:246:27: Error return value of `(*encoding/json.Encoder).Encode` is not checked (errcheck)
  	json.NewEncoder(w).Encode(resp)
  	                         ^
  Error: services/bank-service/internal/repository/exchange_provider.go:88:23: Error return value of `resp.Body.Close` is not checked (errcheck)
  	defer resp.Body.Close()
  	                     ^
  Error: services/bank-service/internal/worker/account_notification.go:51:18: Error return value of `conn.Close` is not checked (errcheck)
  	defer conn.Close()
  	                ^
  Error: services/bank-service/internal/worker/account_notification.go:57:16: Error return value of `ch.Close` is not checked (errcheck)
  	defer ch.Close()
  	              ^
  Error: services/bank-service/internal/worker/actuary_consumer.go:37:18: Error return value of `conn.Close` is not checked (errcheck)
  	defer conn.Close()
  	                ^
  Error: services/bank-service/internal/worker/actuary_consumer.go:43:16: Error return value of `ch.Close` is not checked (errcheck)
  	defer ch.Close()
  	              ^
  Error: services/bank-service/internal/worker/actuary_consumer.go:101:10: Error return value of `msg.Ack` is not checked (errcheck)
  		msg.Ack(false)
  		       ^
  Error: services/bank-service/internal/worker/actuary_consumer.go:108:10: Error return value of `msg.Ack` is not checked (errcheck)
  		msg.Ack(false)
  		       ^
  Error: services/bank-service/internal/worker/actuary_consumer.go:124:11: Error return value of `msg.Nack` is not checked (errcheck)
  		msg.Nack(false, true)
  		        ^
  Error: services/bank-service/internal/worker/actuary_consumer.go:129:9: Error return value of `msg.Ack` is not checked (errcheck)
  	msg.Ack(false)
  	       ^
  Error: services/bank-service/internal/worker/eodhd_client.go:147:23: Error return value of `resp.Body.Close` is not checked (errcheck)
  	defer resp.Body.Close()
  	                     ^
  Error: services/bank-service/internal/worker/finnhub_client.go:83:23: Error return value of `resp.Body.Close` is not checked (errcheck)
  	defer resp.Body.Close()
  	                     ^
  Error: services/bank-service/internal/worker/notification.go:70:18: Error return value of `conn.Close` is not checked (errcheck)
  	defer conn.Close()
  	                ^
  Error: services/bank-service/internal/worker/notification.go:76:16: Error return value of `ch.Close` is not checked (errcheck)
  	defer ch.Close()
  	              ^
  Error: services/notification-service/internal/smtp/sender.go:89:20: Error return value of `client.Close` is not checked (errcheck)
  	defer client.Close()
  	                  ^
  Error: services/notification-service/internal/transport/rabbitmq_consumer.go:77:13: Error return value of `msg.Nack` is not checked (errcheck)
  				msg.Nack(false, true)
  				        ^
  Error: services/user-service/cmd/server/main.go:58:19: Error return value of `sqlDB.Close` is not checked (errcheck)
  	defer sqlDB.Close()
  	                 ^
  Error: services/user-service/internal/handler/grpc_handler_tx_test.go:52:16: Error return value of `db.Close` is not checked (errcheck)
  	defer db.Close()
  	              ^
  Error: services/user-service/internal/handler/grpc_handler_tx_test.go:87:16: Error return value of `db.Close` is not checked (errcheck)
  	defer db.Close()
  	              ^
  Error: services/user-service/internal/handler/grpc_handler_tx_test.go:112:16: Error return value of `db.Close` is not checked (errcheck)
  	defer db.Close()
  	              ^
  Error: services/user-service/internal/handler/http_handler_test.go:33:31: Error return value of `(*encoding/json.Encoder).Encode` is not checked (errcheck)
  		json.NewEncoder(&buf).Encode(body)
  		                            ^
  Error: services/user-service/internal/transport/swagger.go:21:10: Error return value of `w.Write` is not checked (errcheck)
  		w.Write(swaggerJSON)
  		       ^
  Error: services/user-service/internal/transport/swagger.go:26:13: Error return value of `fmt.Fprint` is not checked (errcheck)
  		fmt.Fprint(w, swaggerUI)
  		          ^
  Error: services/bank-service/internal/config/config.go:98:1: File is not properly formatted (gofmt)
  		HTTPAddr: getEnv("HTTP_ADDR", "0.0.0.0:8082"),
  ^
  Error: services/bank-service/internal/domain/account.go:12:1: File is not properly formatted (gofmt)
  	ErrInvalidCurrency    = errors.New("nevalidna valuta za kategoriju računa")
  ^
  Error: services/bank-service/internal/domain/berza.go:37:1: File is not properly formatted (gofmt)
  	CurrencyName string    // naziv valute, npr. "Američki dolar" — popunjava JOIN u repozitorijumu
  ^
  Error: services/bank-service/internal/config/config_test.go:94:14: S1039: unnecessary use of fmt.Sprintf (staticcheck)
  	expected := fmt.Sprintf(
  	            ^
  Error: services/bank-service/internal/handler/trading_handler.go:345:2: QF1003: could use tagged switch on claims.UserType (staticcheck)
  	if claims.UserType == "CLIENT" {
  	^
  Error: services/bank-service/internal/service/kredit_service_test.go:62:27: QF1005: could expand call to math.Pow (staticcheck)
  	want := math.Round((P*(r*math.Pow(1+r, 1))/(math.Pow(1+r, 1)-1))*100) / 100
  	                         ^
  Error: services/bank-service/internal/transport/redis_client.go:92:9: ST1005: error strings should not be capitalized (staticcheck)
  	return fmt.Errorf("Redis nije konfigurisan — postavi REDIS_URL env varijablu")
  	       ^
  Error: services/bank-service/internal/transport/redis_client.go:96:14: ST1005: error strings should not be capitalized (staticcheck)
  	return nil, fmt.Errorf("Redis nije konfigurisan — postavi REDIS_URL env varijablu")
  	            ^
  Error: services/bank-service/internal/transport/redis_market.go:73:9: ST1005: error strings should not be capitalized (staticcheck)
  	return fmt.Errorf("Redis nije konfigurisan — postavi REDIS_URL env varijablu")
  	       ^
  Error: services/bank-service/tests/bdd/krediti_steps_test.go:108:10: QF1008: could remove embedded field "Mock" from selector (staticcheck)
  	svcMock.Mock.Test(nt)
  	        ^
  Error: services/bank-service/tests/bdd/krediti_steps_test.go:111:11: QF1008: could remove embedded field "Mock" from selector (staticcheck)
  	repoMock.Mock.Test(nt)
  	         ^
  Error: services/bank-service/internal/repository/funds_manager.go:436:24: func (*fundsManager).bankTrezorAccountID is unused (unused)
  func (f *fundsManager) bankTrezorAccountID(ctx context.Context) int64 {
                         ^
  Error: services/bank-service/internal/repository/payment_repository.go:854:6: type paymentHistoryRow is unused (unused)
  type paymentHistoryRow struct {
       ^
  Error: services/bank-service/internal/repository/payment_repository.go:1017:6: func convertPaymentAmount is unused (unused)
  func convertPaymentAmount(fromCurrency, toCurrency string, amount float64) float64 {
       ^
  Error: services/bank-service/internal/worker/yahoo_client.go:12:7: const yahooOptionsURLFmt is unused (unused)
  const yahooOptionsURLFmt = "https://query1.finance.yahoo.com/v6/finance/options/%s"
        ^
  Error: services/bank-service/internal/worker/yahoo_client.go:16:6: type yahooOptionsResp is unused (unused)
  type yahooOptionsResp struct {
       ^
  Error: services/bank-service/internal/worker/yahoo_client.go:20:6: type yahooOptionChain is unused (unused)
  type yahooOptionChain struct {
       ^
  Error: services/bank-service/internal/worker/yahoo_client.go:24:6: type yahooOptionResult is unused (unused)
  type yahooOptionResult struct {
       ^
  Error: services/bank-service/internal/worker/yahoo_client.go:30:6: type yahooQuote is unused (unused)
  type yahooQuote struct {
       ^
  Error: services/bank-service/internal/worker/yahoo_client.go:34:6: type yahooOptionExpiry is unused (unused)
  type yahooOptionExpiry struct {
       ^
  Error: services/bank-service/internal/worker/yahoo_client.go:39:6: type yahooContract is unused (unused)
  type yahooContract struct {
       ^
  Error: services/bank-service/internal/worker/yahoo_client.go:55:6: func fetchYahooOptions is unused (unused)
  func fetchYahooOptions(ctx context.Context, client *http.Client, underlyingSymbol string) (*yahooOptionsResp, error) {
       ^
  50 issues:
  * errcheck: 28
  * gofmt: 3
  * staticcheck: 8
  * unused: 11
  
  Error: issues found
```

## generated files up to date

```
Run go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
  go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
  echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"
  shell: /usr/bin/bash -e {0}
  env:
    GOTOOLCHAIN: local
go: downloading google.golang.org/protobuf v1.36.11
go: downloading google.golang.org/grpc v1.80.0
go: downloading google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.1
go: downloading github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0
go: downloading github.com/grpc-ecosystem/grpc-gateway v1.16.0
go: github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest: github.com/grpc-ecosystem/grpc-gateway/v2@v2.29.0 requires go >= 1.25.0 (running go 1.24.13; GOTOOLCHAIN=local)
Error: Process completed with exit code 1.
```

## test (user-service)

```
Run go test -count=1 ./services/user-service/...
  go test -count=1 ./services/user-service/...
  shell: /usr/bin/bash -e {0}
  env:
    GOTOOLCHAIN: local
go: downloading github.com/stretchr/testify v1.10.0
go: downloading gorm.io/driver/postgres v1.5.7
go: downloading gorm.io/gorm v1.25.9
go: downloading github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0
go: downloading github.com/jackc/pgx/v5 v5.5.5
go: downloading google.golang.org/grpc v1.79.2
go: downloading github.com/golang-jwt/jwt/v5 v5.2.1
go: downloading golang.org/x/crypto v0.46.0
go: downloading github.com/gin-gonic/gin v1.9.1
go: downloading google.golang.org/protobuf v1.36.11
go: downloading github.com/DATA-DOG/go-sqlmock v1.5.2
go: downloading github.com/rabbitmq/amqp091-go v1.10.0
go: downloading github.com/davecgh/go-spew v1.1.1
go: downloading github.com/pmezard/go-difflib v1.0.0
go: downloading github.com/jinzhu/now v1.1.5
go: downloading google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57
go: downloading golang.org/x/net v0.48.0
go: downloading github.com/jackc/pgpassfile v1.0.0
go: downloading github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a
go: downloading golang.org/x/text v0.34.0
go: downloading github.com/stretchr/objx v0.5.2
go: downloading github.com/gin-contrib/sse v0.1.0
go: downloading github.com/mattn/go-isatty v0.0.20
go: downloading google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57
go: downloading gopkg.in/yaml.v3 v3.0.1
go: downloading github.com/jinzhu/inflection v1.0.0
go: downloading github.com/jackc/puddle/v2 v2.2.1
go: downloading golang.org/x/sys v0.39.0
go: downloading github.com/go-playground/validator/v10 v10.20.0
go: downloading github.com/pelletier/go-toml/v2 v2.2.2
go: downloading github.com/ugorji/go/codec v1.2.12
go: downloading golang.org/x/sync v0.19.0
go: downloading github.com/gabriel-vasile/mimetype v1.4.3
go: downloading github.com/go-playground/universal-translator v0.18.1
go: downloading github.com/leodido/go-urn v1.4.0
go: downloading github.com/go-playground/locales v0.14.1
?   	banka-backend/services/user-service/cmd/server	[no test files]
ok  	banka-backend/services/user-service/internal/config	0.015s
?   	banka-backend/services/user-service/internal/database	[no test files]
?   	banka-backend/services/user-service/internal/database/sqlc	[no test files]
ok  	banka-backend/services/user-service/internal/domain	0.004s
2026/04/18 15:47:34 [login] user not found for email="ghost@test.com"
2026/04/18 15:47:34 [login] account inactive: user_id=1 email="user@test.com"
2026/04/18 15:47:35 [login] bcrypt mismatch: user_id=1 hash_len=60 err=crypto/bcrypt: hashedPassword is not the hash of the given password
2026/04/18 15:47:35 [login] DB error fetching user: db error
2026/04/18 15:47:35 [login] failed to load permissions: user_id=1 err=perm error
2026/04/18 15:47:36 [create-client] failed to publish activation event for ok@test.com: rabbitmq down
--- FAIL: TestListClients (0.00s)
    --- FAIL: TestListClients/permission_denied_—_admin_caller (0.00s)
panic: 
	assert: mock: I don't know what to return because the method call was unexpected.
		Either do Mock.On("ListClients").Return(...) first, or remove the ListClients() call.
		This method was unexpected:
			ListClients(*context.valueCtx,domain.ClientFilter)
			0: &context.valueCtx{Context:context.backgroundCtx{emptyCtx:context.emptyCtx{}}, key:"jwt_claims", val:(*auth.AccessClaims)(0xc000403ce0)}
			1: domain.ClientFilter{Name:"", Email:"", Limit:0, Offset:0}
		at: [/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/mocks/mock_client_service.go:50 /home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/internal/handler/grpc_handler.go:1236 /home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/internal/handler/grpc_handler_test.go:1389] [recovered]
	panic: 
	assert: mock: I don't know what to return because the method call was unexpected.
		Either do Mock.On("ListClients").Return(...) first, or remove the ListClients() call.
		This method was unexpected:
			ListClients(*context.valueCtx,domain.ClientFilter)
			0: &context.valueCtx{Context:context.backgroundCtx{emptyCtx:context.emptyCtx{}}, key:"jwt_claims", val:(*auth.AccessClaims)(0xc000403ce0)}
			1: domain.ClientFilter{Name:"", Email:"", Limit:0, Offset:0}
		at: [/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/mocks/mock_client_service.go:50 /home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/internal/handler/grpc_handler.go:1236 /home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/internal/handler/grpc_handler_test.go:1389]

goroutine 135 [running]:
testing.tRunner.func1.2({0xc83620, 0xc000374890})
	/opt/hostedtoolcache/go/1.24.13/x64/src/testing/testing.go:1734 +0x21c
testing.tRunner.func1()
	/opt/hostedtoolcache/go/1.24.13/x64/src/testing/testing.go:1737 +0x35e
panic({0xc83620?, 0xc000374890?})
	/opt/hostedtoolcache/go/1.24.13/x64/src/runtime/panic.go:792 +0x132
github.com/stretchr/testify/mock.(*Mock).fail(0xc00040a960, {0xe2d7d7?, 0x4?}, {0xc0002b54c0?, 0x2?, 0x2?})
	/home/runner/go/pkg/mod/github.com/stretchr/testify@v1.10.0/mock/mock.go:349 +0x125
github.com/stretchr/testify/mock.(*Mock).MethodCalled(0xc00040a960, {0x10a8c59, 0xb}, {0xc00034c280, 0x2, 0x2})
	/home/runner/go/pkg/mod/github.com/stretchr/testify@v1.10.0/mock/mock.go:517 +0x785
github.com/stretchr/testify/mock.(*Mock).Called(0xc00040a960, {0xc00034c280, 0x2, 0x2})
	/home/runner/go/pkg/mod/github.com/stretchr/testify@v1.10.0/mock/mock.go:481 +0x125
banka-backend/services/user-service/mocks.(*MockClientService).ListClients(0xc00040a960, {0xf42500, 0xc0003f9170}, {{0x0, 0x0}, {0x0, 0x0}, 0x0, 0x0})
	/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/mocks/mock_client_service.go:50 +0x117
banka-backend/services/user-service/internal/handler.(*UserHandler).ListClients(0xc0000cfed8, {0xf42500, 0xc0003f9170}, 0xc00040a0f0)
	/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/internal/handler/grpc_handler.go:1236 +0x14b
banka-backend/services/user-service/internal/handler_test.TestListClients.func10(0xc000405180)
	/home/runner/work/EXBanka-2-Backend/EXBanka-2-Backend/services/user-service/internal/handler/grpc_handler_test.go:1389 +0x17e
testing.tRunner(0xc000405180, 0xc00040a8c0)
	/opt/hostedtoolcache/go/1.24.13/x64/src/testing/testing.go:1792 +0xf4
created by testing.(*T).Run in goroutine 130
	/opt/hostedtoolcache/go/1.24.13/x64/src/testing/testing.go:1851 +0x413
FAIL	banka-backend/services/user-service/internal/handler	2.234s
?   	banka-backend/services/user-service/internal/interceptor	[no test files]
?   	banka-backend/services/user-service/internal/repository	[no test files]
ok  	banka-backend/services/user-service/internal/service	1.722s
?   	banka-backend/services/user-service/internal/testutil	[no test files]
?   	banka-backend/services/user-service/internal/transport	[no test files]
ok  	banka-backend/services/user-service/internal/utils	4.641s
?   	banka-backend/services/user-service/mocks	[no test files]
FAIL
Error: Process completed with exit code 1.
```
