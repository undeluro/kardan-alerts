.PHONY: demo up down clean logs scale test bench build web run samples

demo: up ## start everything and send the Appendix A sample events
	@echo "waiting for the API..."
	@until curl -fs http://localhost:8080/healthz >/dev/null; do sleep 1; done
	@./samples/send.sh

up: ## build and start the stack
	docker compose up -d --build

down: ## stop the stack (keeps data)
	docker compose down

clean: ## stop the stack and delete its data
	docker compose down -v

logs:
	docker compose logs -f api worker

scale: ## run 3 worker replicas
	docker compose up -d --scale worker=3

test:
	go test ./...

bench:
	go test -run xxx -bench . ./internal/rules/

web:
	cd web && npm ci && npm run build

build: web
	go build -o bin/kardan ./cmd/kardan

run: ## run the binary locally against the compose infrastructure
	docker compose up -d postgres redis redpanda mailpit
	go run ./cmd/kardan

samples:
	./samples/send.sh
