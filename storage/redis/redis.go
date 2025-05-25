package redis

import (
	"context"
	"strings"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

type RedisStorage struct {
	DB *redis.Client
}

func NewRedisStorage(connString string) (*RedisStorage, error) {
	r := &RedisStorage{}
	if opts, err := redis.ParseURL(connString); err != nil {
		return nil, err
	} else {
		r.DB = redis.NewClient(opts)
		return r, nil
	}
}

func (s *RedisStorage) InsertOutput(ctx context.Context, utxo *engine.Output) (err error) {
	_, err = s.DB.Pipelined(ctx, func(p redis.Pipeliner) error {
		if err := p.HMSet(ctx, outputTopicKey(&utxo.Outpoint, utxo.Topic), outputToTopicMap(utxo)).Err(); err != nil {
			return err
		} else if err := p.HMSet(ctx, outputKey(&utxo.Outpoint), outputToMap(utxo)).Err(); err != nil {
			return err
		} else if err = p.HSet(ctx, BeefKey, utxo.Outpoint.Txid.String(), utxo.Beef).Err(); err != nil {
			return err
		} else if err = p.ZAdd(ctx, outMembershipKey(utxo.Topic), redis.Z{
			Score:  float64(utxo.BlockHeight)*1e9 + float64(utxo.BlockIdx),
			Member: utxo.Outpoint.String(),
		}).Err(); err != nil {
			return err
		}
		return nil
	})
	return
}

func (s *RedisStorage) FindOutput(ctx context.Context, outpoint *overlay.Outpoint, topic *string, spent *bool, includeBEEF bool) (o *engine.Output, err error) {

	o = &engine.Output{
		Outpoint: *outpoint,
	}
	if topic != nil {
		otKey := outputTopicKey(outpoint, *topic)
		if spent != nil {
			if isSpent, err := s.DB.HGet(ctx, otKey, "sp").Bool(); err != nil {
				return nil, err
			} else if isSpent != *spent {
				return nil, nil
			}
		}
		if tm, err := s.DB.HGetAll(ctx, otKey).Result(); err == redis.Nil {
			return nil, nil
		} else if err != nil {
			return nil, err
		} else if tm == nil || len(tm) == 0 {
			return nil, nil
		} else if err := populateOutputTopic(o, tm); err != nil {
			return nil, err
		}
	}
	// m := make(map[string]interface{})
	if m, err := s.DB.HGetAll(ctx, outputKey(outpoint)).Result(); err != nil {
		return nil, err
	} else if m == nil || len(m) == 0 {
		return nil, nil
	} else if err := populateOutput(o, m); err != nil {
		return nil, err
	}
	if includeBEEF {
		if o.Beef, err = s.DB.HGet(ctx, BeefKey, outpoint.Txid.String()).Bytes(); err != nil {
			return nil, err
		}
	}
	return
}

func (s *RedisStorage) FindOutputs(ctx context.Context, outpoints []*overlay.Outpoint, topic string, spent *bool, includeBEEF bool) ([]*engine.Output, error) {
	outputs := make([]*engine.Output, 0, len(outpoints))
	for _, outpoint := range outpoints {
		if output, err := s.FindOutput(ctx, outpoint, &topic, spent, includeBEEF); err != nil {
			return nil, err
		} else {
			outputs = append(outputs, output)
		}
	}
	return outputs, nil
}

func (s *RedisStorage) FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash, includeBEEF bool) ([]*engine.Output, error) {
	iter := s.DB.Scan(ctx, 0, "ot:"+txid.String()+"*", 0).Iterator()
	var outputs []*engine.Output
	for iter.Next(ctx) {
		parts := strings.Split(iter.Val(), ":")
		if outpoint, err := overlay.NewOutpointFromString(parts[1]); err != nil {
			return nil, err
		} else {
			topic := parts[2]
			if output, err := s.FindOutput(ctx, outpoint, &topic, nil, includeBEEF); err != nil {
				return nil, err
			} else if output != nil {
				outputs = append(outputs, output)
			}
		}
	}
	return outputs, nil
}

func (s *RedisStorage) FindUTXOsForTopic(ctx context.Context, topic string, since uint32, includeBEEF bool) ([]*engine.Output, error) {
	if outpoints, err := s.DB.ZRangeByScore(ctx, outMembershipKey(topic), &redis.ZRangeBy{
		Min: "0",
		Max: "inf",
	}).Result(); err != nil {
		return nil, err
	} else {
		outputs := make([]*engine.Output, 0, len(outpoints))
		for _, outpointStr := range outpoints {
			if outpoint, err := overlay.NewOutpointFromString(outpointStr); err != nil {
				return nil, err
			} else if output, err := s.FindOutput(ctx, outpoint, &topic, nil, includeBEEF); err != nil {
				return nil, err
			} else if output != nil {
				outputs = append(outputs, output)
			}
		}
		return outputs, nil
	}
}

func (s *RedisStorage) DeleteOutput(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	_, err := s.DB.Pipelined(ctx, func(p redis.Pipeliner) error {
		if err := p.Del(ctx, outputTopicKey(outpoint, topic)).Err(); err != nil {
			return err
		} else if p.ZRem(ctx, outMembershipKey(topic), outpoint.String()).Err(); err != nil {
			return err
		}
		iter := p.Scan(ctx, 0, "ot:"+outpoint.String()+":*", 0).Iterator()
		if !iter.Next(ctx) {
			if err := p.Del(ctx, outputKey(outpoint)).Err(); err != nil {
				return err
				// } else if p.HSetNX(ctx, "beef", beefKey(&utxo.Outpoint.Txid), utxo.Beef).Err(); err != nil {
				// 	return err
			}
		}
		return nil
	})
	return err
}

func (s *RedisStorage) DeleteOutputs(ctx context.Context, outpoints []*overlay.Outpoint, topic string) error {
	for _, outpoint := range outpoints {
		if err := s.DeleteOutput(ctx, outpoint, topic); err != nil {
			return err
		}
	}
	return nil
}

func (s *RedisStorage) MarkUTXOAsSpent(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	return s.DB.HSet(ctx, outputTopicKey(outpoint, topic), "sp", true).Err()
}

func (s *RedisStorage) MarkUTXOsAsSpent(ctx context.Context, outpoints []*overlay.Outpoint, topic string) error {
	for _, outpoint := range outpoints {
		if err := s.MarkUTXOAsSpent(ctx, outpoint, topic); err != nil {
			return err
		}
	}
	return nil
}

func (s *RedisStorage) UpdateConsumedBy(ctx context.Context, outpoint *overlay.Outpoint, topic string, consumedBy []*overlay.Outpoint) error {
	return s.DB.HSet(ctx, outputTopicKey(outpoint, topic), "cb", outpointsToBytes(consumedBy)).Err()
}

func (s *RedisStorage) UpdateTransactionBEEF(ctx context.Context, txid *chainhash.Hash, beef []byte) error {
	return s.DB.HSetNX(ctx, BeefKey, txid.String(), beef).Err()
}

func (s *RedisStorage) UpdateOutputBlockHeight(ctx context.Context, outpoint *overlay.Outpoint, topic string, blockHeight uint32, blockIndex uint64, ancelliaryBeef []byte) error {
	return s.DB.HSet(ctx, outputTopicKey(outpoint, topic), "h", blockHeight, "i", blockIndex, "ab", ancelliaryBeef).Err()
}

func (s *RedisStorage) InsertAppliedTransaction(ctx context.Context, tx *overlay.AppliedTransaction) error {
	return s.DB.SAdd(ctx, txMembershipKey(tx.Topic), tx.Txid.String()).Err()
}

func (s *RedisStorage) DoesAppliedTransactionExist(ctx context.Context, tx *overlay.AppliedTransaction) (bool, error) {
	return s.DB.SIsMember(ctx, txMembershipKey(tx.Topic), tx.Txid.String()).Result()
}

func (s *RedisStorage) Close() error {
	return s.DB.Close()
}
