package main

import "github.com/b-open-io/bsv21-overlay/sub"

func main() {
	sub.Exec()
	sub.QueueDb.Close()
}
