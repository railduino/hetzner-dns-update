# Makefile für DNS-Update Programm

all: hetzner-dns-update

hetzner-dns-update: *.go
	go mod tidy
	go fmt
	go build -o hetzner-dns-update

check: hetzner-dns-update
	./hetzner-dns-update --verbose

update: hetzner-dns-update
	./hetzner-dns-update --verbose --update

install:
	sudo install hetzner-dns-update /usr/local/bin/hetzner-dns-update

clean:
	rm -f hetzner-dns-update hetzner-dns-update.log
