# Builder
FROM golang:1.24-alpine AS builder

# Instaliramo git i build-base
RUN apk update && apk add --no-cache git build-base

WORKDIR /app

# Kopiramo module (pretpostavka je da su go.mod i go.sum u root-u banka-backend foldera) PROVERITI!!!
COPY go.mod go.sum ./
RUN go mod download

# Kopiramo ostatak izvornog koda
COPY . .

# Uvodimo ARG kako bismo znali putanju do servisa (npr. services/user-service)
ARG SERVICE_PATH

# Kompajliramo specifični mikroservis iz tog foldera
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o microservice ./${SERVICE_PATH}

# Produkcijski kontejner
FROM alpine:latest

# Samo ca-certificates + tzdata (bez dodatnih paketa — manje tačaka otkaza pri apk/DNS tokom builda).
RUN apk --no-cache add ca-certificates tzdata
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /home/appuser/

# Kopiramo binarni fajl
COPY --chown=appuser:appgroup --from=builder /app/microservice .

USER appuser:appgroup

# Izlazemo portove
EXPOSE 8080 50051

CMD ["./microservice"]
