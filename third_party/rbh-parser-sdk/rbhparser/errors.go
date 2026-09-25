package rbhparser

import "errors"

var (
	ErrMalformedCalldata   = errors.New("malformed transaction calldata")
	ErrMalformedLog        = errors.New("malformed log")
	ErrPoolIDMismatch      = errors.New("pool id does not match pool key")
	ErrConflictingPool     = errors.New("conflicting pool registration")
	ErrConflictingCurve    = errors.New("conflicting curve registration")
	ErrConflictingToken    = errors.New("conflicting token registration")
	ErrInvalidRegistration = errors.New("invalid registration")
)
