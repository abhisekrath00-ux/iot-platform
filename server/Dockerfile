FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/api ./cmd/api && CGO_ENABLED=0 go build -o /bin/ingest ./cmd/ingest

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /bin/api /bin/ingest /bin/
COPY migrations /migrations
WORKDIR /
ENTRYPOINT ["/bin/api"]
