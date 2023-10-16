package main

import (
	"encoding/json"
	"fmt"
	"github.com/dgraph-io/badger/v4"
	"time"
)

type piCache struct {
	db      *badger.DB
	timeout time.Duration
}

type piCacheRecord struct {
	Data []byte    `json:"data"`
	At   time.Time `json:"at"`
}

func newCache(dbPath string, timeout time.Duration) (*piCache, error) {
	badgerOpts := badger.DefaultOptions(dbPath)
	badgerOpts.Logger = nil
	db, err := badger.Open(badgerOpts)
	if err != nil {
		return nil, err
	}
	return &piCache{
		db:      db,
		timeout: timeout,
	}, nil
}

func (p *piCache) Set(key string, data []byte) error {
	record := piCacheRecord{
		Data: data,
		At:   time.Now(),
	}
	return p.db.Update(func(txn *badger.Txn) error {
		recordData, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return txn.Set([]byte(key), recordData)
	})
}

func (p *piCache) Get(key string) ([]byte, error) {
	var record piCacheRecord
	err := p.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &record)
		})
		if err != nil {
			return err
		}
		if record.At.Add(p.timeout).Unix() > time.Now().Unix() {
			return nil
		}
		return fmt.Errorf("the data is timeout")
	})
	return record.Data, err
}
