FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o pmg-cloud .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/pmg-cloud .
RUN mkdir -p /data
VOLUME ["/data", "/certs"]
EXPOSE 8443
ENTRYPOINT ["./pmg-cloud"]
CMD ["--addr=:8443", "--data-dir=/data", "--tls-cert=/certs/tls.crt", "--tls-key=/certs/tls.key"]
