package supervisor

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/ioutils"
)

// Option will handle the TransactionFactory options
type Option interface {
	// Apply will apply the transactionFactory options to TransactionFactory
	Apply(TransactionFactory) error
}

type transactionFactoryOptions struct {
	transactionType string
	handler         TransactionHandler
}

// Apply will apply the transactionFactory options to TransactionFactory
func (op *transactionFactoryOptions) Apply(f TransactionFactory) error {
	if factory, ok := f.(*transactionFactory); ok {
		if _, exist := factory.handler[op.transactionType]; exist {
			return ErrTransactionKeyExist

		}
		factory.handler[op.transactionType] = op.handler
		return nil
	}
	return fmt.Errorf("Transaction Option not supported for this factory")
}

// WithOption returns the factory options
func WithOption(transactionType string, handler TransactionHandler) Option {
	return &transactionFactoryOptions{
		handler:         handler,
		transactionType: transactionType,
	}
}

// TransactionHandler is a function type which used to handle different kinds of transaction
type TransactionHandler func(Transaction) error

// TransactionFactory is a interface which describes the transaction factory
type TransactionFactory interface {
	// OpenTransaction creates a new transaction
	OpenTransaction(containerID, transactionType string, metadata ...TransactionMetadata) (Transaction, error)
	// RemoveTransaction removes a transaction from transactionFactory
	RemoveTransaction(int64) error
	// HandleTransaction handles all the transactions for containerID when containerd startup
	HandleTransaction(containerID string) error
	// GetTransaction returns Transaction from id
	GetTransaction(id int64) (Transaction, error)
}

type transactionFactory struct {
	sync.Mutex
	root         string
	id           int64
	handler      map[string]TransactionHandler
	transactions map[int64]Transaction
}

// NewTransactionFactory creates the transaction factory
func NewTransactionFactory(root string, options ...Option) (TransactionFactory, error) {
	factory := &transactionFactory{
		root:         root,
		id:           1,
		handler:      make(map[string]TransactionHandler),
		transactions: make(map[int64]Transaction),
	}
	for _, option := range options {
		if err := option.Apply(factory); err != nil {
			return nil, err
		}
	}
	return factory, nil
}

func (factory *transactionFactory) newTransactionID() int64 {
	factory.Lock()
	factory.id++
	id := factory.id
	factory.Unlock()
	return id
}

func (factory *transactionFactory) addTransaction(t Transaction) error {
	factory.Lock()
	defer factory.Unlock()
	if _, exist := factory.transactions[t.TransactionID()]; exist {
		return fmt.Errorf("transaction id %d exist", t.TransactionID())
	}
	factory.transactions[t.TransactionID()] = t
	return nil
}

// RemoveTransaction removes a transaction from transactionFactory
func (factory *transactionFactory) RemoveTransaction(id int64) error {
	factory.Lock()
	delete(factory.transactions, id)
	factory.Unlock()
	return nil
}

// GetTransaction returns Transaction from id
func (factory *transactionFactory) GetTransaction(id int64) (Transaction, error) {
	factory.Lock()
	defer factory.Unlock()
	if transaction, exist := factory.transactions[id]; exist {
		return transaction, nil
	}
	return nil, fmt.Errorf("transaction id %s does not exist", id)
}

// OpenTransaction creates a new transaction
func (factory *transactionFactory) OpenTransaction(containerID, transactionType string, metadata ...TransactionMetadata) (Transaction, error) {
	if _, exist := factory.handler[transactionType]; !exist {
		return nil, ErrTransactionTypeNotSupported
	}

	transaction := &transaction{
		root:            factory.root,
		containerID:     containerID,
		transactionType: transactionType,
		transactionID:   factory.newTransactionID(),
		Metadata:        make(map[string]interface{}),
		factory:         factory,
	}
	for _, m := range metadata {
		if err := m.Apply(transaction); err != nil {
			return nil, err
		}
	}

	os.MkdirAll(filepath.Dir(transaction.filePath()), 0600)
	jsonData, err := json.Marshal(transaction)
	if err != nil {
		return nil, err
	}
	if err := ioutils.AtomicWriteFile(transaction.filePath(), jsonData, 0600); err != nil {
		return nil, err
	}
	if err := factory.addTransaction(transaction); err != nil {
		return nil, err
	}

	return transaction, nil
}

// HandleTransaction handles all the transactions for containerID when containerd startup
func (factory *transactionFactory) HandleTransaction(containerID string) error {
	var transactions []*transaction
	for transactionType := range factory.handler {
		path := filepath.Join(factory.root, containerID, transactionType)
		if _, err := os.Stat(path); err == nil {
			files, err := ioutil.ReadDir(path)
			if err != nil {
				if !os.IsNotExist(err) {
					logrus.Errorf("containerd: failed to read path %s, with error: %v", path, err)
				}
				continue
			}

			for _, file := range files {
				if id, err := getTransactionID(file.Name()); err == nil {
					transactionFile := filepath.Join(path, file.Name())
					jsonData, err := ioutil.ReadFile(transactionFile)
					if err != nil {
						logrus.Errorf("containerd: failed to read file %s, with error: %v", transactionFile, err)
						continue
					}
					transaction := &transaction{
						transactionID:   id,
						containerID:     containerID,
						root:            factory.root,
						transactionType: transactionType,
						Metadata:        make(map[string]interface{}),
						factory:         factory,
					}
					if err := json.Unmarshal(jsonData, transaction); err != nil {
						logrus.Errorf("containerd: failed to decode transaction, with error: %v", err)
						continue
					}

					transactions = append(transactions, transaction)
				}
			}
		}
	}

	// here to sort transaction by Timestamp, we must make sure the sequence of transactions.
	sort.Sort(SortedTransaction(transactions))
	for _, t := range transactions {
		tType := t.transactionType
		logrus.Debugf("containerd: handling transaction for container %s, type %s, transactionID %d", t.containerID, t.transactionType, t.transactionID)
		if err := factory.handler[tType](t); err != nil {
			logrus.Errorf("containerd: transaction handler for %s, type %s error: %v", t.containerID, t.transactionType, err)
		}
		t.Close()
	}
	return nil
}
