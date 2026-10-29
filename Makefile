# Go parameters
GOCMD=go
GORUN=$(GOCMD) run
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test

ENTRY=./cmd/server

SWAG=swag
SWAG_DOC=$(SWAG) init -g $(ENTRY)/main.go -o ./docs

SOPS=sops
CONFIG=config.yaml
CONFIG_ENC=config.enc.yaml

# Docker
DOCKER_IMAGE=materm-web-portal-backend
DOCKER_PORT=3000

# --- test infrastructure --------------------------------------------------------------------------
# Tests that need a database or Redis carry the build tag `dbtest`, so plain `make test` never touches any of
# them. They run ONLY on the scratch Postgres + Redis (127.0.0.1:55433 / :56380) that belongs to the admin
# backend repository (its scripts/testdb.sh). This repo does not start or stop them: `make testdb-up` asks
# the admin repo, whose script returns early when the containers already run, and `test-db` loads that
# repo's git-ignored .env.test into the shell of the test command without printing it. The guards in
# pkg/testutil refuse database `materm`, ports 5432/6379 and any cluster without the scratch marker.
ADMIN_REPO ?= ../materm-admin-backend
TESTDB_ENV ?= $(ADMIN_REPO)/.env.test

# $(1)=env file, $(2)=command. Values are never echoed.
define WITH_ENV
@if [ -f "$(1)" ]; then set -a; . "$(1)"; set +a; fi; $(2)
endef

.PHONY: run build swag test test-coverage lint install encrypt decrypt docker-test-build docker-test-run docker-test docker-test-stop \
	testdb-up testdb-status test-db check-config

run:
	$(GORUN) $(ENTRY)

build:
	$(GOBUILD) -o bin/app $(ENTRY)

swag:
	$(SWAG_DOC)

# unit tests of every package (no database, no Redis)
test:
	LOG_FILE=off $(shell go env GOPATH)/bin/gotestsum --format testname ./...

# the scratch Postgres/Redis must be up (`make testdb-up`); creates random throw-away databases there
test-db:
	$(call WITH_ENV,$(TESTDB_ENV),LOG_FILE=off $(GOTEST) -tags=dbtest ./...)

testdb-up:
	$(MAKE) -C $(ADMIN_REPO) testdb-up

testdb-status:
	$(MAKE) -C $(ADMIN_REPO) testdb-status

# fails (printing key paths, never values) when config.yaml contains a value: it must stay a blank template in git
check-config:
	$(ADMIN_REPO)/scripts/check-config-blank.sh $(CONFIG)
	$(ADMIN_REPO)/scripts/check-config-blank.sh deploy/config.blank.yaml

test-coverage:
	@$(GOTEST) -coverprofile=coverage.out -coverpkg=github.com/materm-organization/materm-web-portal-backend/modules/... ./... 2>&1 | grep "^ok"
	@$(GOCMD) tool cover -func=coverage.out | grep -E "(services|mapper|^total)" | \
		awk 'BEGIN{W=50;cur=""} \
		/^total/{tp=$$3;next} \
		{split($$1,a,"/");n=length(a);pkg=a[n-1];mod=""; \
		 for(i=1;i<=n;i++){if(a[i]=="modules"){mod=a[i+1];break}} \
		 ml=mod"/"pkg; \
		 if(ml!=cur){if(cur!="")printf"\n";printf"\n  \033[1m%s\033[0m\n",ml;cur=ml} \
		 pct=$$3+0;fi=int(pct/100*W);em=W-fi; \
		 bar="";for(i=0;i<fi;i++)bar=bar"█";for(i=0;i<em;i++)bar=bar"░"; \
		 cl=(pct>=80)?"\033[32m":(pct>=50)?"\033[33m":"\033[31m"; \
		 printf"  %s%s\033[0m  %3.0f%%  %s\n",cl,bar,pct,$$2} \
		END{pct=tp+0;fi=int(pct/100*W);em=W-fi; \
		 bar="";for(i=0;i<fi;i++)bar=bar"█";for(i=0;i<em;i++)bar=bar"░"; \
		 printf"\n  \033[1mTOTAL\033[0m\n"; \
		 printf"  \033[36m%s\033[0m  \033[1m%.1f%%\033[0m covered\n\n",bar,pct}'

# build tag so that the DB-backed test files (dbtest) are linted too
lint:
	$(shell go env GOPATH)/bin/golangci-lint run --build-tags=dbtest ./...

install:
	$(GOCMD) mod download
	$(GOCMD) install github.com/swaggo/swag/cmd/swag@latest
	$(GOCMD) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(GOCMD) install gotest.tools/gotestsum@latest

encrypt:
	$(SOPS) --encrypt $(CONFIG) > $(CONFIG_ENC)

decrypt:
	$(SOPS) --decrypt $(CONFIG_ENC) > $(CONFIG)

docker-test-build:
	docker build -t $(DOCKER_IMAGE) .

docker-test-run:
	docker run --rm -p $(DOCKER_PORT):$(DOCKER_PORT) --name $(DOCKER_IMAGE) $(DOCKER_IMAGE)

docker-test:
	docker build -t $(DOCKER_IMAGE) . && docker run --rm -p $(DOCKER_PORT):$(DOCKER_PORT) --name $(DOCKER_IMAGE) $(DOCKER_IMAGE)

docker-test-stop:
	docker stop $(DOCKER_IMAGE)
