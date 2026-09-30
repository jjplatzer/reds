package stars

import "errors"

// STARS response and error messages use the exact operator-facing wording
// from TI 6191.409. Keep them as errors so all command handlers can return the
// same reusable values and the Preview Area can display err.Error() verbatim.
var (
	ErrSTARSCommandFormat   = errors.New("FORMAT")
	ErrSTARSRangeLimit      = errors.New("RANGE LIMIT")
	ErrSTARSIllegalValue    = errors.New("ILL VALUE")
	ErrSTARSIllegalPosition = errors.New("ILL POS")
	ErrSTARSIllegalTrack    = errors.New("ILL TRK")
	ErrSTARSIllegalFunction = errors.New("ILL FNCT")
	ErrSTARSNoTrack         = errors.New("NO TRK")
	ErrSTARSCapacity        = errors.New("CAPACITY")
	ErrSTARSRBLID           = errors.New("RBL ID")
	ErrSTARSNoFlight        = errors.New("NO FLIGHT")
	ErrSTARSDuplicateBeacon = errors.New("DUP BCN")
	ErrSTARSDuplicateACID   = errors.New("DUP NEW ID")
)
