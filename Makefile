build: vet
	go build
.PHONY: build

fmt:
	goimports -l -w .
.PHONY: fmt

lint: fmt
	golint
.PHONY: lint

vet: fmt
	go vet
	go vet -vettool=$$(which shadow)
.PHONY: vet

clean:
	$(RM) pisim
.PHONY: clean
