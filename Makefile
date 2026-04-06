.PHONY: build test lint clean run-chat run-web fmt vet

# === Basic Info ===
MODULE   := github.com/SOULOFCINDERS/agent
BINARY   := agent
CMD_DIR  := ./cmd/agent

# === Build ===
build:
	go build -o $(BINARY) $(CMD_DIR)

# === Test ===
test:
	go test ./...

test-v:
	go test -v ./...

test-cover:
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# === Code Quality ===
fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint: vet
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

# === Run ===
run-chat:
	go run $(CMD_DIR) chat

run-chat-mock:
	go run $(CMD_DIR) chat --mock

run-web:
	go run $(CMD_DIR) web --port 8080

run:
	go run $(CMD_DIR) run $(ARGS)

# === Clean ===
clean:
	rm -f $(BINARY) coverage.out coverage.html
	go clean -cache -testcache

# === Help ===
help:
	@echo "Available targets:"
	@echo "  build          Build the project"
	@echo "  test           Run all tests"
	@echo "  test-v         Run all tests (verbose)"
	@echo "  test-cover     Run tests with coverage report"
	@echo "  fmt            Format code"
	@echo "  vet            Static analysis"
	@echo "  lint           Lint code (requires golangci-lint)"
	@echo "  run-chat       Start interactive chat"
	@echo "  run-chat-mock  Start mock mode chat"
	@echo "  run-web        Start web server (port 8080)"
	@echo "  run            Run single task: make run ARGS=calc:1+1"
	@echo "  clean          Clean build artifacts"
