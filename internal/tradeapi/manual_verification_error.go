package tradeapi

import (
	"database/sql"
	"errors"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
)

// A verified absent/invalid durable input is a rejection. A failed read is not
// proof of invalid input: preserve its cause so HTTP returns sanitized 503 and
// callers can distinguish unavailability without changing any financial state.
func manualVerificationError(err error, code string) error {
	var input *manualInputError
	if err == nil || errors.As(err, &input) || errors.Is(err, sql.ErrNoRows) ||
		errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrPolicy) || errors.Is(err, ErrWallet) ||
		errors.Is(err, chainquery.ErrValidation) || errors.Is(err, chainquery.ErrObservation) || errors.Is(err, chainquery.ErrExpired) {
		return rejectManualInput(code)
	}
	return err
}
