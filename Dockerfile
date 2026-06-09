FROM golang:1.26.4 AS builder
WORKDIR /workspace

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /twinward ./cmd/twinward

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /twinward /twinward
USER 65532:65532
ENTRYPOINT ["/twinward"]
