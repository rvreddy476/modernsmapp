package main

import "errors"

// errInternalKeyRequired is why production refuses to boot without
// INTERNAL_SERVICE_KEY.
var errInternalKeyRequired = errors.New("INTERNAL_SERVICE_KEY is required in production: " +
	"search handlers trust the gateway's identity headers")

// requireInternalKey is the production boot gate (X-L1). Outside
// production an unset key only warns, as before (see main).
func requireInternalKey(key string, production bool) error {
	if key == "" && production {
		return errInternalKeyRequired
	}
	return nil
}
