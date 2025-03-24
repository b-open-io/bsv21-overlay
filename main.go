package main

import (
	"github.com/4chain-ag/go-overlay-services/pkg/engine"
	"github.com/b-open-io/bsv21-overlay/topics"
)

func main() {
	e := engine.Engine{
		Managers: map[string]engine.TopicManager{
			"bsv21": topics.NewBsv21TopicManager([]string{
				"ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0", //MNEE
			}),
		},
	}
}
