module github.com/b-open-io/bsv21-overlay

go 1.24.3

require (
	github.com/GorillaPool/go-junglebus v0.2.14
	github.com/b-open-io/overlay v0.0.0-20250115201712-ca366bce7ebf
	github.com/bitcoin-sv/go-templates v0.0.0-00010101000000-000000000000
	github.com/bsv-blockchain/go-overlay-services v0.1.1
	github.com/bsv-blockchain/go-sdk v1.2.6
	github.com/gofiber/fiber/v2 v2.52.8
	github.com/joho/godotenv v1.5.1
	github.com/mattn/go-sqlite3 v1.14.30
	github.com/redis/go-redis/v9 v9.12.1
	github.com/stretchr/testify v1.10.0
)

require (
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/centrifugal/centrifuge-go v0.10.10 // indirect
	github.com/centrifugal/protocol v0.16.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-resty/resty/v2 v2.16.5 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/oapi-codegen/runtime v1.1.1 // indirect
	github.com/philhofer/fwd v1.1.3-0.20240916144458-20a13a1f6b7c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/segmentio/encoding v0.5.1 // indirect
	github.com/shadowspore/fossil-delta v0.0.0-20241213113458-1d797d70cbe3 // indirect
	github.com/tinylib/msgp v1.2.5 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.59.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	go.mongodb.org/mongo-driver/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.40.0 // indirect
	golang.org/x/net v0.42.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
	golang.org/x/text v0.27.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/bsv-blockchain/go-overlay-services => github.com/bsv-blockchain/go-overlay-services v0.1.2-0.20250808182921-aeae02752891

// replace github.com/b-open-io/overlay => ../overlay

replace github.com/b-open-io/overlay => github.com/b-open-io/overlay v0.0.0-20250815185749-ca366bce7ebf

replace github.com/bitcoin-sv/go-templates => github.com/b-open-io/go-templates v0.0.0-20250611003449-d3d47c4c4967
