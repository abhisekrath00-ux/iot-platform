FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/api ./cmd/api && CGO_ENABLED=0 go build -o /bin/ingest ./cmd/ingest && CGO_ENABLED=0 go build -o /bin/mcp ./cmd/mcp

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /bin/api /bin/ingest /bin/mcp /bin/
COPY migrations /migrations
WORKDIR /
ENTRYPOINT ["/bin/api"]
