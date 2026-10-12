package participant

import "crypto/rand"

// ID is a globally unique UUID v4 identifying one participant instance.
type ID [16]byte

// Alias identifies a participant instance. Name is reusable; ID is not.
type Alias struct {
	ID   ID
	Name string
}

func createAlias(name string) Alias {
	var id ID
	rand.Read(id[:])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return Alias{ID: id, Name: name}
}
