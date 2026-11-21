.PHONY: demo up down clean logs scale test bench build web run samples load load-storm load-series k6

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

load: ## steady load: 500 events/s for 30 s, every event a new alert and SMS
	docker compose run --rm loadgen -scenario steady -rate 500 -duration 30s

load-storm: ## market-open storm: 100 -> 1000 -> 100 events/s
	docker compose run --rm loadgen -scenario storm -rate 100 -duration 60s

load-series: ## the measured series behind loadtest/results.md (run with 1 and with 3 workers)
	./loadtest/run-series.sh "$(shell docker compose ps -q worker | wc -l | tr -d ' ') worker(s)"

k6: ## k6 view of the HTTP ingest edge
	docker run --rm -i --network kardan_default -e KARDAN_URL=http://api:8080 grafana/k6:0.57.0 run - < loadtest/k6-ingest.js
