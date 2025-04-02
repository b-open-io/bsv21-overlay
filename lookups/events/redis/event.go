package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/b-open-io/bsv21-overlay/lookups/events"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/redis/go-redis/v9"
)

type RedisEventLookup struct {
	db      *redis.Client
	storage engine.Storage
}

func eventKey(event string) string {
	return "ev:" + event
}

func outpointEventsKey(outpoint *overlay.Outpoint) string {
	return "oe:" + outpoint.String()
}

func NewRedisEventLookup(connString string, storage engine.Storage) (*RedisEventLookup, error) {
	r := &RedisEventLookup{
		storage: storage,
	}
	if opts, err := redis.ParseURL(connString); err != nil {
		return nil, err
	} else {
		r.db = redis.NewClient(opts)
		return r, nil
	}
}

func (l *RedisEventLookup) SaveEvent(ctx context.Context, outpoint *overlay.Outpoint, event string, height uint32, idx uint64) error {
	var score float64
	if height > 0 {
		score = float64(height)*1e9 + float64(idx)
	} else {
		score = float64(time.Now().UnixNano())
	}
	_, err := l.db.Pipelined(ctx, func(p redis.Pipeliner) error {
		op := outpoint.String()
		if err := p.ZAdd(ctx, eventKey(event), redis.Z{
			Score:  score,
			Member: op,
		}).Err(); err != nil {
			return err
		} else if err := p.SAdd(ctx, outpointEventsKey(outpoint), event).Err(); err != nil {
			return err
		}
		p.Publish(ctx, event, fmt.Sprintf("%f:%s", score, op))
		return nil
	})
	return err

}
func (l *RedisEventLookup) SaveEvents(ctx context.Context, outpoint *overlay.Outpoint, events []string, height uint32, idx uint64) error {
	var score float64
	if height > 0 {
		score = float64(height)*1e9 + float64(idx)
	} else {
		score = float64(time.Now().UnixNano())
	}
	op := outpoint.String()
	_, err := l.db.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, event := range events {
			if err := p.ZAdd(ctx, eventKey(event), redis.Z{
				Score:  score,
				Member: op,
			}).Err(); err != nil {
				return err
			} else if err := p.SAdd(ctx, outpointEventsKey(outpoint), event).Err(); err != nil {
				return err
			}
			p.Publish(ctx, event, op)
		}
		return nil
	})
	return err
}
func (l *RedisEventLookup) Close() {
	if l.db != nil {
		l.db.Close()
	}
}

func (l *RedisEventLookup) Lookup(ctx context.Context, q *lookup.LookupQuestion) (answer *lookup.LookupAnswer, err error) {
	question := &events.Question{}
	if err := json.Unmarshal(q.Query, question); err != nil {
		return nil, err
	}

	startScore := float64(question.From.Height)*1e9 + float64(question.From.Idx)
	var outpoints []string
	if question.Events != nil && len(question.Events) > 0 {
		join := events.JoinTypeIntersect
		if question.JoinType != nil {
			join = *question.JoinType
		}
		keys := make([]string, len(question.Events))
		for _, event := range question.Events {
			keys = append(keys, eventKey(event))
		}
		results := make([]redis.Z, 0)
		switch join {
		case events.JoinTypeIntersect:
			results, err = l.db.ZInterWithScores(ctx, &redis.ZStore{
				Aggregate: "MIN",
				Keys:      keys,
			}).Result()
		case events.JoinTypeUnion:
			results, err = l.db.ZUnionWithScores(ctx, redis.ZStore{
				Aggregate: "MIN",
				Keys:      keys,
			}).Result()
		case events.JoinTypeDifference:
			results, err = l.db.ZDiffWithScores(ctx, keys...).Result()
		default:
			return nil, errors.New("invalid join type")
		}
		if err != nil {
			return nil, err
		}
		slices.SortFunc(results, func(a, b redis.Z) int {
			if question.Reverse {
				if a.Score > b.Score {
					return 1
				} else if a.Score < b.Score {
					return -1
				}
			} else {
				if a.Score < b.Score {
					return 1
				} else if a.Score > b.Score {
					return -1
				}
			}
			return 0
		})
		for _, item := range results {
			if question.Limit > 0 && len(outpoints) >= question.Limit {
				break
			} else if question.Reverse && item.Score < startScore {
				outpoints = append(outpoints, item.Member.(string))
			} else if !question.Reverse && item.Score > startScore {
				outpoints = append(outpoints, item.Member.(string))
			}
		}
	} else if question.Event != "" {
		query := redis.ZRangeArgs{
			Key:     eventKey(question.Event),
			Start:   fmt.Sprintf("(%f", startScore),
			Stop:    "+inf",
			ByScore: true,
			Rev:     question.Reverse,
			Count:   int64(question.Limit),
		}
		if outpoints, err = l.db.ZRangeArgs(ctx, query).Result(); err != nil {
			return nil, err
		}
	}
	answer = &lookup.LookupAnswer{
		Type: lookup.AnswerTypeOutputList,
	}
	for _, op := range outpoints {
		if outpoint, err := overlay.NewOutpointFromString(op); err != nil {
			return nil, err
		} else if outpoint != nil {
			if output, err := l.storage.FindOutput(ctx, outpoint, nil, nil, true); err != nil {
				return nil, err
			} else if output != nil {
				if beef, _, _, err := transaction.ParseBeef(output.Beef); err != nil {
					return nil, err
				} else {
					if output.AncillaryBeef != nil {
						if err = beef.MergeBeefBytes(output.AncillaryBeef); err != nil {
							return nil, err
						}
					}
					if beefBytes, err := beef.AtomicBytes(&outpoint.Txid); err != nil {
						return nil, err
					} else {
						answer.Outputs = append(answer.Outputs, &lookup.OutputListItem{
							OutputIndex: output.Outpoint.OutputIndex,
							Beef:        beefBytes,
						})
					}
				}
			}
		}
	}
	return answer, nil

}

func (l *RedisEventLookup) OutputSpent(ctx context.Context, outpoint *overlay.Outpoint, _ string) error {
	return l.db.SAdd(ctx, eventKey("spent"), outpoint.String()).Err()
}

func (l *RedisEventLookup) OutputsSpent(ctx context.Context, outpoints []*overlay.Outpoint, _ string) error {
	args := make([]interface{}, 0, len(outpoints))
	for _, outpoint := range outpoints {
		args = append(args, outpoint.Bytes())
	}
	return l.db.SAdd(ctx, eventKey("spent"), args...).Err()
}

func (l *RedisEventLookup) OutputDeleted(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	op := outpoint.String()
	if events, err := l.db.SMembers(ctx, outpointEventsKey(outpoint)).Result(); err != nil {
		return err
	} else if len(events) == 0 {
		return nil
	} else {
		_, err := l.db.Pipelined(ctx, func(p redis.Pipeliner) error {
			for _, event := range events {
				if err := p.ZRem(ctx, eventKey(event), op).Err(); err != nil {
					return err
				}
			}
			return p.Del(ctx, outpointEventsKey(outpoint)).Err()
		})
		return err
	}
}

func (l *RedisEventLookup) OutputBlockHeightUpdated(ctx context.Context, outpoint *overlay.Outpoint, height uint32, idx uint64) error {
	var score float64
	if height > 0 {
		score = float64(height)*1e9 + float64(idx)
	} else {
		score = float64(time.Now().UnixNano())
	}
	op := outpoint.String()
	if events, err := l.db.SMembers(ctx, outpointEventsKey(outpoint)).Result(); err != nil {
		return err
	} else if len(events) == 0 {
		return nil
	} else {
		_, err := l.db.Pipelined(ctx, func(p redis.Pipeliner) error {
			for _, event := range events {
				if err := p.ZAdd(ctx, eventKey(event), redis.Z{
					Score:  score,
					Member: op,
				}).Err(); err != nil {
					return err
				}
			}
			return nil
		})
		return err
	}
}

func (l *RedisEventLookup) GetDocumentation() string {
	return "Events lookup"
}

func (l *RedisEventLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "Events",
	}
}
