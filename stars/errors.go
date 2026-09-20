package stars

import "errors"

// STARS response and error messages use the exact operator-facing wording
// from TI 6191.409. Keep them as errors so all command handlers can return the
// same reusable values and the Preview Area can display err.Error() verbatim.
var (
	ErrSTARSCommandFormat = errors.New("FORMAT")
	ErrSTARSRangeLimit    = errors.New("RANGE LIMIT")
)
