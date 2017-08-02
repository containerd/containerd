package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// PauseTransaction stands for the pause transaction
	PauseTransaction = "transaction-pause"
	// ResumeTransaction stands for the resume transaction
	ResumeTransaction = "transaction-resume"
	// ExitTransaction stands for the signal transaction
	ExitTransaction = "transaction-exit"
)

var (
	// ErrTransactionTypeNotSupported means the transaction type is not supported error
	ErrTransactionTypeNotSupported = errors.New("Transaction type not supported")
	// ErrTransactionKeyExist means the transaction type conflicts
	ErrTransactionKeyExist = errors.New("Transaction key exists")
)

func getTransactionID(path string) (int64, error) {
	ids := strings.SplitN(path, "-", 2)
	if len(ids) < 2 {
		return -1, fmt.Errorf("containerd: invalid path %s to parse transaction id", path)
	}
	return strconv.ParseInt(ids[1], 10, 64)
}

// Transaction is the interface for transaction abstract
type Transaction interface {
	// Close will close a transaction
	Close() error
	// ContainerID returns the container id of the transaction
	ContainerID() string
	// TransactionID returns the transaction ID of the transaction.
	// Each transaction has a unique ID.
	TransactionID() int64
	// MetaData returns the metadata for the transaction.
	// It will hold the transaction parameter of the transaction:
	// For example:
	//    Sginal transaction need to save signal nunber and PID parameters to transaction.
	MetaData() map[string]interface{}
	// TimeStamp returns the time of the transaction opened
	TimeStamp() time.Time
}

type transaction struct {
	transactionID   int64
	transactionType string
	root            string
	containerID     string
	factory         TransactionFactory
	Metadata        map[string]interface{} `json:"metadata"`
	Timestamp       time.Time              `json:"timestamp"`
}

func (t *transaction) filePath() string {
	return filepath.Join(t.root, t.containerID, t.transactionType, fmt.Sprintf("transaction-%d", t.transactionID))
}

// Close will close a transaction
func (t *transaction) Close() error {
	os.RemoveAll(t.filePath())
	return t.factory.RemoveTransaction(t.transactionID)
}

// ContainerID returns the container id of the transaction
func (t *transaction) ContainerID() string {
	return t.containerID
}

// TransactionID returns the transaction ID of the transaction.
func (t *transaction) TransactionID() int64 {
	return t.transactionID
}

// MetaData returns the metadata for the transaction.
func (t *transaction) MetaData() map[string]interface{} {
	return t.Metadata
}

// TimeStamp returns the time of the transaction opened
func (t *transaction) TimeStamp() time.Time {
	return t.Timestamp
}

// TransactionMetadata will handle the metadata parameter
type TransactionMetadata interface {
	// Apply will apply the transaction metada options to Transaction.
	Apply(Transaction) error
}

type transactionMetadata struct {
	key   string
	value interface{}
}

// WithTransactionMetadata handles the transaction metada options.
func WithTransactionMetadata(key string, value interface{}) TransactionMetadata {
	return &transactionMetadata{
		key:   key,
		value: value,
	}
}

// Apply will apply the transaction metada options to Transaction.
func (metadata *transactionMetadata) Apply(t Transaction) error {
	if transaction, ok := t.(*transaction); ok {
		if _, exist := transaction.Metadata[metadata.key]; exist {
			return ErrTransactionKeyExist
		}
		transaction.Metadata[metadata.key] = metadata.value
		return nil
	}
	return fmt.Errorf("Transaction Option not supported for this transaction")
}

// TransactionTimestamp will handle the transaction timestamp
type TransactionTimestamp interface {
	Apply(Transaction) error
}

type transactionTimestamp struct {
	timestamp time.Time
}

// Apply will apply the transaction timestamp option for Transaction.
func (ts *transactionTimestamp) Apply(t Transaction) error {
	if transaction, ok := t.(*transaction); ok {
		transaction.Timestamp = ts.timestamp
		return nil
	}
	return fmt.Errorf("Transaction timestamp not supported for this transaction")
}

// WithTransactionTimestamp handles the transaction timestamp options.
func WithTransactionTimestamp(ts time.Time) TransactionTimestamp {
	return &transactionTimestamp{
		timestamp: ts,
	}
}

// SortedTransaction is used to sort the transaction.
// Maybe there are more than one transactions opened before containerd creash,
// We must make sure the sequence of the transactions to be executed.
type SortedTransaction []*transaction

// Len returns the lenghth of the SortedTransaction
func (s SortedTransaction) Len() int {
	return len([]*transaction(s))
}

// Swap swaps two elements in SortedTransaction
func (s SortedTransaction) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// Less returns if the first element is 'less than' the second one
func (s SortedTransaction) Less(i, j int) bool {
	return s[i].Timestamp.Before(s[j].Timestamp)
}
