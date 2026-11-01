.PHONY: test vet lint run

test:
	go test ./...

vet:
	go vet ./...

lint:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

run:
	go run .
