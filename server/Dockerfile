FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/api ./cmd/api && CGO_ENABLED=0 go build -o /bin/ingest ./cmd/ingest && CGO_ENABLED=0 go build -o /bin/mcp ./cmd/mcp && CGO_ENABLED=0 go build -o /bin/tenantctl ./cmd/tenantctl

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /bin/api /bin/ingest /bin/mcp /bin/tenantctl /bin/
COPY migrations /migrations
WORKDIR /
# compose overrides this with /bin/ingest or /bin/mcp per service
CMD ["/bin/api"]
