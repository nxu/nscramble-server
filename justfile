image := "nscramble-server"
dev_key := "dev-key-0123456789"

default:
    @just --list

# Run the tests
test:
    go vet ./...
    go test ./...

# Run locally on http://127.0.0.1:8788 with API key "dev-key-0123456789" and ./data/nscramble.sqlite
run:
    NSCRAMBLE_API_KEY={{dev_key}} NSCRAMBLE_ADDR=127.0.0.1:8788 NSCRAMBLE_DB=data/nscramble.sqlite go run ./cmd/nscramble-server

# Build a static binary into ./bin
build:
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/nscramble-server ./cmd/nscramble-server

# Build the Docker image (default: for an x86-64 Linux server)
docker-build platform="linux/amd64":
    docker buildx build --platform {{platform}} -t {{image}} --load .

# Run the image locally on http://127.0.0.1:8788, data in the "nscramble-data" volume
docker-run platform="linux/amd64":
    docker run --rm --platform {{platform}} -p 127.0.0.1:8788:8080 -e NSCRAMBLE_API_KEY={{dev_key}} -v nscramble-data:/data {{image}}
