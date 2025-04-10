# Makefile für DNS-Update Programm

BINARY=hetzner_dns_update

build: main.go
	go build -o $(BINARY) main.go

install:
	sudo install $(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -f $(BINARY)
