package binding

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"github.com/boltdb/bolt"
	"github.com/jeffjenkins/mq/amqp"
	"github.com/jeffjenkins/mq/gen"
	"github.com/jeffjenkins/mq/persist"
	"regexp"
	"strings"
)

type Binding struct {
	gen.BindingState
	topicMatcher *regexp.Regexp
}

var topicRoutingPatternPattern, _ = regexp.Compile(`^((\w+|\*|#)(\.(\w+|\*|#))*|)$`)

func (binding *Binding) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"queueName":    binding.QueueName,
		"exchangeName": binding.ExchangeName,
		"key":          binding.Key,
		"arguments":    binding.Arguments,
	})
}

func (binding *Binding) Equals(other *Binding) bool {
	if other == nil || binding == nil {
		return false
	}
	return binding.QueueName == other.QueueName &&
		binding.ExchangeName == other.ExchangeName &&
		binding.Key == other.Key
}

func (binding *Binding) Depersist(db *bolt.DB) error {
	return persist.DepersistOne(db, "bindings", string(binding.Id))
}

func (binding *Binding) DepersistBoltTx(tx *bolt.Tx) error {
	bucket, err := tx.CreateBucketIfNotExists([]byte("bindings"))
	if err != nil { // pragma: nocover
		// If we're hitting this it means the disk is full, the db is readonly,
		// or something else has gone irrecoverably wrong
		panic(fmt.Sprintf("create bucket: %s", err))
	}
	return persist.DepersistOneBoltTx(bucket, string(binding.Id))
}

func NewBinding(queueName string, exchangeName string, key string, arguments *amqp.Table, topic bool) (*Binding, error) {
	var re *regexp.Regexp = nil
	// Topic routing key
	if topic {
		if !topicRoutingPatternPattern.MatchString(key) {
			return nil, fmt.Errorf("Topic exchange routing key can only have a-zA-Z0-9, or # or *")
		}
		var parts = strings.Split(key, ".")
		for i, part := range parts {
			if part == "*" {
				parts[i] = `[^\.]+`
			} else if part == "#" {
				parts[i] = ".*"
			} else {
				parts[i] = regexp.QuoteMeta(parts[i])
			}
		}
		expression := "^" + strings.Join(parts, `\.`) + "$"
		var err error = nil
		re, err = regexp.Compile(expression)
		if err != nil { // pragma: nocover
			// This is impossible to get to based on the earlier
			// code, so we panic and don't count it for coverage
			panic(fmt.Sprintf("Could not compile regex: '%s'", expression))
		}
	}

	return &Binding{
		BindingState: gen.BindingState{
			Id:           calcId(queueName, exchangeName, key, arguments),
			QueueName:    queueName,
			ExchangeName: exchangeName,
			Key:          key,
			Arguments:    arguments,
		},
		topicMatcher: re,
	}, nil
}

func (b *Binding) Persist(db *bolt.DB) error {
	return persist.PersistOne(db, "bindings", string(b.Id), b)
}

func (b *Binding) MatchDirect(message *amqp.BasicPublish) bool {
	return message.Exchange == b.ExchangeName && b.Key == message.RoutingKey
}

func (b *Binding) MatchFanout(message *amqp.BasicPublish) bool {
	return message.Exchange == b.ExchangeName
}

func (b *Binding) MatchTopic(message *amqp.BasicPublish) bool {
	var ex = b.ExchangeName == message.Exchange
	var match = b.topicMatcher.MatchString(message.RoutingKey)
	return ex && match
}

// Calculate an ID by encoding the QueueBind call that created this binding and
// taking a hash of it.
func calcId(queueName string, exchangeName string, key string, arguments *amqp.Table) []byte {
	var method = &amqp.QueueBind{
		Queue:      queueName,
		Exchange:   exchangeName,
		RoutingKey: key,
		Arguments:  arguments,
	}
	var buffer = bytes.NewBuffer(make([]byte, 0))
	method.Write(buffer)
	// trim off the first four bytes, they're the class/method, which we
	// already know
	var value = buffer.Bytes()[4:]
	// bindings aren't named, so we hash the bytes we encoded
	hash := sha1.New()
	hash.Write(value)
	return []byte(hash.Sum(nil))
}
