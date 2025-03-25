module github.com/b-open-io/bsv21-overlay

go 1.24.1

require (
	github.com/4chain-ag/go-overlay-services v0.0.0-00010101000000-000000000000
	github.com/GorillaPool/go-junglebus v0.2.14
	github.com/bitcoin-sv/go-templates v0.0.0-00010101000000-000000000000
	github.com/bsv-blockchain/go-sdk v1.1.22
	github.com/joho/godotenv v1.5.1
	github.com/mattn/go-sqlite3 v1.14.24
)

require (
	github.com/centrifugal/centrifuge-go v0.10.2 // indirect
	github.com/centrifugal/protocol v0.10.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/segmentio/encoding v0.3.6 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	google.golang.org/protobuf v1.30.0 // indirect
)

replace github.com/4chain-ag/go-overlay-services => ../go-overlay-services

replace github.com/bsv-blockchain/go-sdk => ../go-sdk

replace github.com/bitcoin-sv/go-templates => ../go-templates
