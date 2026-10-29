VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE   ?= ghcr.io/lioilsources/promohub
JODA    ?= joda
STACK   ?= stack
# Throwaway Postgres for `make test-db`.
TEST_PG ?= postgres://promohub:promohub@127.0.0.1:55432/promohub?sslmode=disable

.PHONY: build test test-db vet generate check run docker push deploy deploy-config pg-up pg-down

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/promohub ./cmd/promohub

test:
	go test ./...

# Unit tests plus the Postgres integration tests against a throwaway container.
test-db: pg-up
	PROMOHUB_TEST_DATABASE_URL='$(TEST_PG)' go test -count=1 ./...

pg-up:
	docker run -d --rm --name promohub-test-pg -p 55432:5432 \
		-e POSTGRES_USER=promohub -e POSTGRES_PASSWORD=promohub -e POSTGRES_DB=promohub postgres:17-alpine >/dev/null
	until docker exec promohub-test-pg pg_isready -U promohub -d promohub >/dev/null 2>&1; do sleep 1; done

pg-down:
	docker rm -f promohub-test-pg >/dev/null

vet:
	go vet ./...

# Regenerate sqlc code after editing internal/store/query.sql or migrations.
generate:
	sqlc generate

# Validate apps.yaml, accounts.yaml and rules.yaml in the repo root.
check: build
	CONFIG_DIR=. ./bin/promohub check

# Local run: needs .env (DATABASE_URL pointing at a local Postgres, e.g. `make pg-up` + TEST_PG).
run: build
	set -a && . ./.env && set +a && CONFIG_DIR=. ./bin/promohub serve

docker:
	docker build --platform linux/amd64 --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

push: docker
	docker push $(IMAGE):$(VERSION)
	docker push $(IMAGE):latest

# Copy config to JODA (secrets in .env are managed there by hand).
deploy-config: check
	ssh $(JODA) 'mkdir -p ~/$(STACK)/promohub/config'
	scp apps.yaml accounts.yaml rules.yaml $(JODA):$(STACK)/promohub/config/

deploy: push deploy-config
	ssh $(JODA) 'cd ~/$(STACK) && docker compose pull promohub && docker compose up -d promohub'
