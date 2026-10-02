package httpapi

import (
	"context"
	"net/http"
	"time"
)

// contextDetached keeps request values but not request cancellation, bounded
// by its own timeout.
func contextDetached(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), d)
}
