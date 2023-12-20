package main

import (
	"encoding/json"
	"fmt"
	"github.com/dgraph-io/badger/v4"
	"time"
)

type piCache struct {
	//db      *badger.DB
	timeout time.Duration
	dbPath  string
}

type piCacheRecord struct {
	Data []byte    `json:"data"`
	At   time.Time `json:"at"`
}

func newCache(dbPath string, timeout time.Duration) (*piCache, error) {
	return &piCache{
		dbPath:  dbPath,
		timeout: timeout,
	}, nil
}

func (p *piCache) openDb() (*badger.DB, error) {
	badgerOpts := badger.DefaultOptions(p.dbPath)
	badgerOpts.Logger = nil
	var err error
	for i := 0; i < 10; i++ {
		db, connErr := badger.Open(badgerOpts)
		if connErr == nil {
			return db, nil
		}
		err = connErr
		time.Sleep(time.Millisecond * 500)
	}
	return nil, err
}

func (p *piCache) Set(key string, data []byte) error {
	db, err := p.openDb()
	if err != nil {
		return err
	}
	record := piCacheRecord{
		Data: data,
		At:   time.Now(),
	}
	return db.Update(func(txn *badger.Txn) error {
		recordData, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return txn.Set([]byte(key), recordData)
	})
}

func (p *piCache) Get(key string) ([]byte, error) {
	db, err := p.openDb()
	if err != nil {
		return nil, err
	}
	var record piCacheRecord
	err = db.View(func(txn *badger.Txn) error {
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

func (p *piCache) Clear() error {
	db, err := p.openDb()
	if err != nil {
		return err
	}
	return db.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.Key()
			err := txn.Delete(k)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
