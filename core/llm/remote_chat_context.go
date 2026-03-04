package llm

import "fmt"

type remoteChatItem struct {
	item ChatItem
	prev *remoteChatItem
	next *remoteChatItem
}

type RemoteChatContext struct {
	head     *remoteChatItem
	tail     *remoteChatItem
	idToItem map[string]*remoteChatItem
}

func NewRemoteChatContext() *RemoteChatContext {
	return &RemoteChatContext{
		idToItem: make(map[string]*remoteChatItem),
	}
}

func (c *RemoteChatContext) ToChatCtx() *ChatContext {
	items := make([]ChatItem, 0)
	current := c.head
	for current != nil {
		items = append(items, current.item)
		current = current.next
	}
	return &ChatContext{Items: items}
}

func (c *RemoteChatContext) Get(itemID string) ChatItem {
	if node, ok := c.idToItem[itemID]; ok {
		return node.item
	}
	return nil
}

func (c *RemoteChatContext) Insert(previousItemID *string, message ChatItem) error {
	itemID := message.GetID()

	if _, exists := c.idToItem[itemID]; exists {
		return fmt.Errorf("item with ID %s already exists", itemID)
	}

	newNode := &remoteChatItem{item: message}

	if previousItemID == nil {
		if c.head != nil {
			newNode.next = c.head
			c.head.prev = newNode
		} else {
			c.tail = newNode
		}
		c.head = newNode
		c.idToItem[itemID] = newNode
		return nil
	}

	prevNode, ok := c.idToItem[*previousItemID]
	if !ok {
		return fmt.Errorf("previous_item_id `%s` not found", *previousItemID)
	}

	newNode.prev = prevNode
	newNode.next = prevNode.next
	prevNode.next = newNode

	if newNode.next != nil {
		newNode.next.prev = newNode
	} else {
		c.tail = newNode
	}

	c.idToItem[itemID] = newNode
	return nil
}

func (c *RemoteChatContext) Delete(itemID string) error {
	node, ok := c.idToItem[itemID]
	if !ok {
		return fmt.Errorf("item_id `%s` not found", itemID)
	}

	prevNode := node.prev
	nextNode := node.next

	if c.head == node {
		c.head = nextNode
		if c.head != nil {
			c.head.prev = nil
		}
	} else {
		if prevNode != nil {
			prevNode.next = nextNode
		}
	}

	if c.tail == node {
		c.tail = prevNode
		if c.tail != nil {
			c.tail.next = nil
		}
	} else {
		if nextNode != nil {
			nextNode.prev = prevNode
		}
	}

	delete(c.idToItem, itemID)
	return nil
}
