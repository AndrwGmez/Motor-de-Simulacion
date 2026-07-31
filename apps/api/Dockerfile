FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/flowverse-api ./cmd/api

FROM alpine:3.22.5@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
COPY --from=build /out/flowverse-api /flowverse-api
RUN addgroup -S -g 10001 flowverse \
    && adduser -S -D -H -u 10001 -G flowverse flowverse
EXPOSE 8080
USER flowverse:flowverse
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=10 \
  CMD wget -qO- http://127.0.0.1:8080/health/ready || exit 1
ENTRYPOINT ["/flowverse-api"]
