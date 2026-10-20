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

.PHONY: run build swag test test-coverage lint install encrypt decrypt docker-test-build docker-test-run docker-test docker-test-stop

run:
	$(GORUN) $(ENTRY)

build:
	$(GOBUILD) -o bin/app $(ENTRY)

swag:
	$(SWAG_DOC)

test:
	$(shell go env GOPATH)/bin/gotestsum --format testname ./tests/...

test-coverage:
	@$(GOTEST) -coverprofile=coverage.out -coverpkg=go-fiber/modules/... ./tests/... 2>&1 | grep "^ok"
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

lint:
	$(shell go env GOPATH)/bin/golangci-lint run ./...

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
