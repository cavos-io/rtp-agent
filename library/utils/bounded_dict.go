package utils

import (
	"container/list"
	"fmt"
	"sync"
)

type BoundedDict[K comparable, V any] struct {
	maxSize   int
	evictList *list.List
	items     map[K]*list.Element
	mu        sync.Mutex
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

func NewBoundedDict[K comparable, V any](maxSize int) *BoundedDict[K, V] {
	if maxSize <= 0 {
		panic(fmt.Errorf("maxSize must be greater than 0"))
	}
	return &BoundedDict[K, V]{
		maxSize:   maxSize,
		evictList: list.New(),
		items:     make(map[K]*list.Element),
	}
}

func (c *BoundedDict[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ent, ok := c.items[key]; ok {
		ent.Value.(*entry[K, V]).value = value
		return
	}

	ent := &entry[K, V]{key, value}
	element := c.evictList.PushFront(ent)
	c.items[key] = element

	if c.evictList.Len() > c.maxSize {
		c.removeOldest()
	}
}

func (c *BoundedDict[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ent, ok := c.items[key]; ok {
		return ent.Value.(*entry[K, V]).value, true
	}

	var zero V
	return zero, false
}

func (c *BoundedDict[K, V]) UpdateValue(key K, update func(V) V) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ent, ok := c.items[key]; ok {
		kv := ent.Value.(*entry[K, V])
		kv.value = update(kv.value)
		return kv.value, true
	}

	var zero V
	return zero, false
}

func (c *BoundedDict[K, V]) SetOrUpdate(key K, factory func() V, update func(V) V) V {
	c.mu.Lock()
	defer c.mu.Unlock()

	var value V
	if ent, ok := c.items[key]; ok {
		kv := ent.Value.(*entry[K, V])
		value = kv.value
	} else {
		value = factory()
		ent := &entry[K, V]{key, value}
		element := c.evictList.PushFront(ent)
		c.items[key] = element
		if c.evictList.Len() > c.maxSize {
			c.removeOldest()
		}
	}

	if update != nil {
		value = update(value)
		if ent, ok := c.items[key]; ok {
			ent.Value.(*entry[K, V]).value = value
		}
	}
	return value
}

func (c *BoundedDict[K, V]) PopIf(predicate func(V) bool) (K, V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if predicate == nil {
		return c.popElement(c.evictList.Back())
	}

	for element := c.evictList.Front(); element != nil; element = element.Next() {
		kv := element.Value.(*entry[K, V])
		if predicate(kv.value) {
			return c.popElement(element)
		}
	}

	var zeroKey K
	var zeroValue V
	return zeroKey, zeroValue, false
}

func (c *BoundedDict[K, V]) removeOldest() {
	c.popElement(c.evictList.Back())
}

func (c *BoundedDict[K, V]) popElement(ent *list.Element) (K, V, bool) {
	var zeroKey K
	var zeroValue V
	if ent == nil {
		return zeroKey, zeroValue, false
	}
	c.evictList.Remove(ent)
	kv := ent.Value.(*entry[K, V])
	delete(c.items, kv.key)
	return kv.key, kv.value, true
}
