package blockchain

import (
	"bytes"
	"encoding/hex"
	"log"

	"github.com/dgraph-io/badger"
)

type UTXOSet struct {
	BlockChain *Blockchain
}

var (
	utxoPrefix   = []byte("utxo-")
	prefixLength = len(utxoPrefix)
)

func (u *UTXOSet) DeleteByPrefix(prefix []byte) {
	deleteKeys := func(keysForDelete [][]byte) error {
		if err := u.BlockChain.Database.Update(func(txn *badger.Txn) error {
			for _, key := range keysForDelete {
				if err := txn.Delete(key); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		return nil
	}

	collectSize := 100000
	u.BlockChain.Database.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false // just fetches the keys not the data as we are just conerned about the keys
		it := txn.NewIterator(opts)
		defer it.Close()

		keysForDelete := make([][]byte, 0, collectSize) // create a slice of bytes with first dimention 0 and second dimension 100000
		keysCollected := 0
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := it.Item().KeyCopy(nil)
			keysForDelete = append(keysForDelete, key)
			keysCollected++
			if keysCollected == collectSize {
				if err := deleteKeys(keysForDelete); err != nil {
					log.Panic(err)
				}
				keysForDelete = make([][]byte, 0, collectSize)
				keysCollected = 0
			}
		}
		if keysCollected > 0 {
			if err := deleteKeys(keysForDelete); err != nil {
				log.Panic(err)
			}
		}
		return nil
	})
}

func (u UTXOSet) Reindex() {
	db := u.BlockChain.Database

	u.DeleteByPrefix(utxoPrefix)
	UTXO := u.BlockChain.FindUTXO()
	err := db.Update(func(txn *badger.Txn) error {
		for txId, outs := range UTXO {
			key, err := hex.DecodeString(txId)
			if err != nil {
				return err
			}
			key = append(utxoPrefix, key...)
			err = txn.Set(key, outs.Serialize())
			Handle(err)
		}
		return nil
	})
	Handle(err)

}
func (u *UTXOSet) Update(block *Block) {
	db := u.BlockChain.Database
	var v []byte
	err := db.Update(func(txn *badger.Txn) error {
		for _, tx := range block.Transactions {
			if tx.IsCoinbase() == false {
				for _, in := range tx.Inputs {
					updatedOuts := TxOutputs{}
					inId := append(utxoPrefix, in.ID...)
					item, err := txn.Get(inId)
					Handle(err)
					err = item.Value(func(val []byte) error {
						v = append([]byte{}, val...)
						return nil
					})
					Handle(err)
					outs := DeserializeOutputs(v)
					for outIdx, out := range outs.Outputs {
						if outIdx != in.Out {
							updatedOuts.Outputs = append(updatedOuts.Outputs, out)
						}
					}
					if len(updatedOuts.Outputs) == 0 {
						if err := txn.Delete(inId); err != nil {
							log.Panic(err)
						}
					} else {
						if err := txn.Set(inId, updatedOuts.Serialize()); err != nil {
							log.Panic(err)
						}
					}
				}
			}

			newOutputs := TxOutputs{}
			for _, out := range tx.Outputs {
				newOutputs.Outputs = append(newOutputs.Outputs, out)

			}
			txID := append(utxoPrefix, tx.ID...)
			if err := txn.Set(txID, newOutputs.Serialize()); err != nil {
				log.Panic(err)
			}
		}

		return nil
	})
	Handle(err)
}
func (u UTXOSet) CountTransactions() int {
	db := u.BlockChain.Database
	counter := 0

	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(utxoPrefix); it.ValidForPrefix(utxoPrefix); it.Next() {
			counter++
		}

		return nil
	})

	Handle(err)

	return counter
}
func (u *UTXOSet) FindUnspentTransactions(pubKeyHash []byte) []TxOutput {
	var UTXOs []TxOutput
	db := u.BlockChain.Database
	var v []byte

	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(utxoPrefix); it.ValidForPrefix(utxoPrefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				v = append([]byte{}, val...)
				return nil
			})
			Handle(err)
			outs := DeserializeOutputs(v)
			for _, out := range outs.Outputs {
				if out.IsLockedWithKey(pubKeyHash) {
					UTXOs = append(UTXOs, out)
				}
			}

		}

		return nil
	})
	Handle(err)

	return UTXOs
}
func (u UTXOSet) FindSpendableOutputs(pubKeyHash []byte, amount int) (int, map[string][]int) {
	unspentOuts := make(map[string][]int)
	accumulated := 0
	var v []byte
	db := u.BlockChain.Database

	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(utxoPrefix); it.ValidForPrefix(utxoPrefix); it.Next() {
			item := it.Item()
			k := item.Key()
			err := item.Value(func(val []byte) error {
				v = append([]byte{}, val...)
				return nil
			})
			Handle(err)
			k = bytes.TrimPrefix(k, utxoPrefix)
			txID := hex.EncodeToString(k)
			outs := DeserializeOutputs(v)

			for outIdx, out := range outs.Outputs {
				if out.IsLockedWithKey(pubKeyHash) && accumulated < amount {
					accumulated += out.Value
					unspentOuts[txID] = append(unspentOuts[txID], outIdx)
				}
			}
		}
		return nil
	})
	Handle(err)
	return accumulated, unspentOuts
}
