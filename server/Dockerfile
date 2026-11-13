FROM golang:1.26-alpine AS edge-build
RUN apk add --no-cache bash zip dpkg python3
WORKDIR /src
COPY edge ./edge
COPY scripts/build-edge.sh scripts/edge-manifest.py ./scripts/
ARG EDGE_VERSION=0.2.1
RUN bash scripts/build-edge.sh "$EDGE_VERSION"

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server .
RUN CGO_ENABLED=0 go build -o /bin/setupctl ./cmd/setupctl && CGO_ENABLED=0 go build -o /bin/cachecheck ./cmd/cachecheck && CGO_ENABLED=0 go build -o /bin/api ./cmd/api && CGO_ENABLED=0 go build -o /bin/ingest ./cmd/ingest && CGO_ENABLED=0 go build -o /bin/mcp ./cmd/mcp && CGO_ENABLED=0 go build -o /bin/tenantctl ./cmd/tenantctl

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /bin/api /bin/ingest /bin/mcp /bin/tenantctl /bin/cachecheck /bin/setupctl /bin/
COPY --from=edge-build /src/dist/edge /edge-packages
COPY server/migrations /migrations
WORKDIR /
CMD ["/bin/api"]
