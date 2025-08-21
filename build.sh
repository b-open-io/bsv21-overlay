go build -o sub.run cmd/sub/sub.go
go build -o queue.run cmd/queue/queue.go
go build -o process.run cmd/process/process.go
go build -o server.run ./cmd/server/
go build -o config.run cmd/config/config.go