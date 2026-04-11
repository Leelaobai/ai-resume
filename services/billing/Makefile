.PHONY: build run test proto migrate-up migrate-down

build:
	go build -o bin/billing ./cmd/

run:
	go run ./cmd/

test:
	go test ./... -v -count=1

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       proto/billing/v1/billing.proto

migrate-up:
	cd migrations && for f in *.up.sql; do mysql -h127.0.0.1 -uroot -proot123 tadpoles_billing < "$$f"; done

migrate-down:
	cd migrations && for f in $$(ls *.down.sql | sort -r); do mysql -h127.0.0.1 -uroot -proot123 tadpoles_billing < "$$f"; done
