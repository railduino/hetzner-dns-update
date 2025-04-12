# Makefile für DNS-Update Programm

BINARY=hetzner_dns_update

build: main.go
	go mod tidy
	go fmt
	go build -o $(BINARY) main.go

run: build
	sudo ./$(BINARY) --verbose

update: build
	sudo ./$(BINARY) --verbose --update

install:
	sudo install $(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -f $(BINARY)
