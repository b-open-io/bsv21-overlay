package redis

import (
	"strconv"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
)

func outputToMap(output *engine.Output) map[string]interface{} {
	m := make(map[string]interface{})
	m["h"] = output.BlockHeight
	m["i"] = output.BlockIdx
	m["st"] = output.Satoshis
	m["sc"] = output.Script.Bytes()
	// m["sp"] = output.Spent
	return m
}

func outputToTopicMap(output *engine.Output) map[string]interface{} {
	m := make(map[string]interface{})
	m["t"] = output.Topic
	m["sp"] = output.Spent
	if len(output.OutputsConsumed) > 0 {
		m["c"] = outpointsToBytes(output.OutputsConsumed)
	}
	if len(output.ConsumedBy) > 0 {
		m["cb"] = outpointsToBytes(output.ConsumedBy)
	}
	if len(output.AncillaryTxids) > 0 {
		m["at"] = chainhashesToBytes(output.AncillaryTxids)
	}
	m["ab"] = output.AncillaryBeef
	return m
}

func populateOutput(o *engine.Output, m map[string]string) error {
	if height, err := strconv.ParseUint(m["h"], 10, 32); err != nil {
		return err
	} else if o.BlockIdx, err = strconv.ParseUint(m["i"], 10, 64); err != nil {
		return err
	} else if o.Satoshis, err = strconv.ParseUint(m["st"], 10, 64); err != nil {
		return err
	} else {
		o.BlockHeight = uint32(height)

	}
	o.Script = script.NewFromBytes([]byte(m["sc"]))
	return nil
}

func populateOutputTopic(o *engine.Output, m map[string]string) (err error) {
	o.Topic = m["t"]
	o.Spent = m["sp"] == "1"
	o.OutputsConsumed = bytesToOutpoints([]byte(m["c"]))
	o.ConsumedBy = bytesToOutpoints([]byte(m["cb"]))
	o.AncillaryTxids = bytesToChainhashes([]byte(m["at"]))
	o.AncillaryBeef = []byte(m["ab"])
	return
}

func outpointsToBytes(outpoints []*overlay.Outpoint) []byte {
	b := make([]byte, 36*len(outpoints))
	for i, outpoint := range outpoints {
		copy(b[i*36:], outpoint.Bytes())
	}
	return b
}
func bytesToOutpoints(b []byte) []*overlay.Outpoint {
	outpoints := make([]*overlay.Outpoint, 0, len(b)/36)
	for i := 0; i < len(b); i += 36 {
		outpoints = append(outpoints, overlay.NewOutpointFromBytes([36]byte(b[i:i+36])))
	}
	return outpoints
}
func chainhashesToBytes(hashes []*chainhash.Hash) []byte {
	b := make([]byte, 32*len(hashes))
	for i, hash := range hashes {
		copy(b[i*32:], hash.CloneBytes())
	}
	return b
}
func bytesToChainhashes(b []byte) []*chainhash.Hash {
	hashes := make([]*chainhash.Hash, 0, len(b)/32)
	for i := 0; i < len(b); i += 32 {
		if txid, err := chainhash.NewHash(b[i : i+32]); err != nil {
			return nil
		} else {
			hashes = append(hashes, txid)
		}
	}
	return hashes
}
