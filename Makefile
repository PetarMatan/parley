ROOT     := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
AIC_WORK ?= $(ROOT)/work
export AIC_WORK

.PHONY: build test test-go test-node vectors setup up down clean reset

build:
	mkdir -p $(AIC_WORK)/bin
	cd $(ROOT)/inbox && go build -o $(AIC_WORK)/bin/ ./cmd/...

test: test-go test-node

test-go:
	cd $(ROOT)/inbox && go test ./...

test-node:
	cd $(ROOT)/plugin && node --test
	cd $(ROOT)/quarantine && node --test
	cd $(ROOT)/canaries && node --test

# vectors.json is generated; edit schema/gen-vectors.mjs, then run this.
vectors:
	node $(ROOT)/schema/gen-vectors.mjs > $(ROOT)/schema/vectors.json

setup:
	bash $(ROOT)/sandbox/setup.sh

up:
	docker compose -f $(ROOT)/deploy/local/compose.yaml up -d

down:
	docker compose -f $(ROOT)/deploy/local/compose.yaml down

# Removes build output only. `make reset` deletes the whole work dir.
clean:
	rm -rf $(AIC_WORK)/bin

reset:
	bash $(ROOT)/sandbox/reset.sh

# Testbed targets (testbed/Makefile) when that file exists.
-include $(ROOT)/testbed/Makefile
