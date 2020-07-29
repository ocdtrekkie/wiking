.PHONY: dev build release test clean

all: dev

dev: build
	./wiki --data ./checkout/ --repo ./repo.git/

build: clean
	go build

release:
	@./tools/release.sh

test:
	go test ./...

clean:
	rm -rf wiki
