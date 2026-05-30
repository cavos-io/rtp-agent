package ipc

import (
	"encoding/binary"
	"encoding/json"
	"io"
)

func WriteMessage(w io.Writer, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func ReadMessage(r io.Reader) (Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return Message{}, err
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return Message{}, err
	}

	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}
