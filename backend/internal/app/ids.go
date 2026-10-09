package app

import "github.com/google/uuid"

func newID(prefix string) string {
	return prefix + "_" + uuid.NewString()
}
