# syntax=docker/dockerfile:1
FROM golang:1.25.13-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go test ./... && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/facility-consumer ./cmd/facility-consumer && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/outbox-publisher ./cmd/outbox-publisher
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /commerce-finance-api
COPY --from=build /out/migrate /commerce-finance-migrate
COPY --from=build /out/facility-consumer /commerce-finance-facility-consumer
COPY --from=build /out/outbox-publisher /commerce-finance-outbox-publisher
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/commerce-finance-api"]
