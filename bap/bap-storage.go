package bap

import (
	"context"
	"encoding/json"

	"github.com/BitcoinSchema/go-bap-indexer/types"
	"github.com/redis/go-redis/v9"
)

type bapStorage struct {
	db *redis.Client
}

func identityKey(id string) string {
	return "bap:id:" + id
}

func attestKey(hash string) string {
	return "bap:att:" + hash
}

func NewBapStorage(connString string) (*bapStorage, error) {
	b := &bapStorage{}
	if opts, err := redis.ParseURL(connString); err != nil {
		return nil, err
	} else {
		b.db = redis.NewClient(opts)
		return b, nil
	}
}

func (b *bapStorage) FindIdentity(id string) (*types.Identity, error) {
	identity := &types.Identity{
		Identity: id,
	}
	if j, err := b.db.JSONGet(context.Background(), identityKey(id), "$").Result(); err == redis.Nil {
		return nil, nil
	} else if err != nil {
		return nil, err
	} else if err := json.Unmarshal([]byte(j), identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func (b *bapStorage) FindAttest(hash string) (*types.Attestation, error) {
	att := &types.Attestation{
		URN: hash,
	}
	if j, err := b.db.JSONGet(context.Background(), attestKey(hash), "$").Result(); err == redis.Nil {
		return nil, nil
	} else if err != nil {
		return nil, err
	} else if err := json.Unmarshal([]byte(j), att); err != nil {
		return nil, err
	}
	return att, nil
}
