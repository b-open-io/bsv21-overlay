module github.com/b-open-io/bsv21-overlay

go 1.24.1

require (
	github.com/4chain-ag/go-overlay-services v0.0.0-00010101000000-000000000000
	github.com/bitcoin-sv/go-templates v0.0.0-00010101000000-000000000000
	github.com/bsv-blockchain/go-sdk v1.1.22
)

require (
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/crypto v0.36.0 // indirect
)

replace github.com/4chain-ag/go-overlay-services => ../go-overlay-services

replace github.com/bsv-blockchain/go-sdk => ../go-sdk

replace github.com/bitcoin-sv/go-templates => ../go-templates
