package redis

import "github.com/bsv-blockchain/go-sdk/overlay"

func outputTopicKey(outpoint *overlay.Outpoint, topic string) string {
	return "ot:" + outpoint.String() + ":" + topic
}

func outputKey(outpoint *overlay.Outpoint) string {
	return "o:" + outpoint.String()
}

var BeefKey = "beef"

func outMembershipKey(topic string) string {
	return "om:" + topic
}

func txMembershipKey(topic string) string {
	return "tm:" + topic
}
