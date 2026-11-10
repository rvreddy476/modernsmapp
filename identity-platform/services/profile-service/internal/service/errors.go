package service

import (
	"errors"
	"time"
)

// Sentinel errors for the refusals a client may be told about. The HTTP layer
// maps each one with errors.Is to a stable code and fixed copy; any other
// error is an internal failure and is answered with a generic 500, so store,
// driver and verifier text never reaches a client.
var (
	ErrCannotFollowSelf = errors.New("cannot follow yourself")
	ErrCannotFollowUser = errors.New("cannot follow this user")
	ErrCannotBlockSelf  = errors.New("cannot block yourself")

	ErrInvalidModule = errors.New("invalid module: must be postbook, posttube, or postgram")

	ErrUsernameEmpty  = errors.New("username cannot be empty")
	ErrUsernameTaken  = errors.New("username already taken")
	ErrHandleCooldown = errors.New("handle change on cooldown")
)

// HandleCooldownError is a handle change refused because the last change is
// still inside its cooldown. It matches ErrHandleCooldown under errors.Is and
// carries the time the next change is allowed.
type HandleCooldownError struct {
	Until time.Time
}

func (e *HandleCooldownError) Error() string {
	return ErrHandleCooldown.Error() + " until " + e.Until.Format(time.RFC3339)
}

func (e *HandleCooldownError) Is(target error) bool { return target == ErrHandleCooldown }
