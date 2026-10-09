package v1

import "github.com/google/uuid"

func newProfileID() string {
	return "profile_" + uuid.NewString()
}
