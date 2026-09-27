# syntax=docker/dockerfile:1

# Build natively on the build machine and cross-compile for the target (pure Go, no cgo).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/nscramble-server ./cmd/nscramble-server \
    && mkdir /out/data

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/nscramble-server /nscramble-server
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV NSCRAMBLE_DB=/data/nscramble.sqlite \
    NSCRAMBLE_ADDR=:8080
VOLUME /data
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/nscramble-server", "healthcheck"]
ENTRYPOINT ["/nscramble-server"]
