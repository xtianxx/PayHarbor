package constants

// PRODUCT_NAME is the user-visible brand used in the Server/AppName headers.
// APPLICATION_NAME is this application's internal identifier; it has no external
// consumer (env vars, API key prefixes, cookie names, metric prefixes and the
// module path stay untouched for compatibility).
const (
	APPLICATION_NAME = "PAYHARBOR"
	PRODUCT_NAME     = "PayHarbor"
	PRODUCT_VERSION  = "1.0.0"
)

const (
	DEFAULT_LIMIT = 20
	MAXIMUM_LIMIT = 100
)
