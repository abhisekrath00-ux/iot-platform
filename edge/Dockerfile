# Edge agent image (for x86 test rigs; on Axon deploy the native ARM binary,
# see docs/deployment.md).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/edge-agent ./cmd/edge-agent

FROM alpine:3.20
RUN adduser -D hexmon
USER hexmon
COPY --from=build /bin/edge-agent /bin/edge-agent
ENTRYPOINT ["/bin/edge-agent"]
