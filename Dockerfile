FROM golang:1.23 AS builder
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/sidecar ./cmd/sidecar

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /out/sidecar /sidecar
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/sidecar"]
