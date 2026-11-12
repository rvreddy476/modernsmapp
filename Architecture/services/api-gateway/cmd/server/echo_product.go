package main

import "strings"

// Echo (premium short notes, docs/designs/echo-contract.md) is parked at the
// edge exactly like Dating and Doorstep: closed unless ECHO_PUBLIC_ENABLED is
// "true", except to the verified users on ECHO_PILOT_USER_IDS. An empty or
// unset allowlist lets nobody through; an invalid entry refuses boot in
// production. post-service and feed-service keep their own ECHO_ENABLED
// switch behind this one.
//
// Both prefixes are matched on path segments by dormantProductRefuses, so
// closing /v1/feed/notes leaves /v1/feed/home and the rest of /v1/feed alone.
const (
	echoPublicEnv = "ECHO_PUBLIC_ENABLED"
	echoPilotEnv  = "ECHO_PILOT_USER_IDS"
)

var echoPrefixes = []string{"/v1/notes", "/v1/feed/notes"}

// echoDormantProduct builds Echo's entry for dormantProductsFromEnv.
func echoDormantProduct(getenv func(string) string, production bool) (dormantProduct, error) {
	pilot, err := parsePilotUserIDs(echoPilotEnv, getenv(echoPilotEnv), production)
	if err != nil {
		return dormantProduct{}, err
	}
	return dormantProduct{
		prefixes:   echoPrefixes,
		enabled:    strings.EqualFold(getenv(echoPublicEnv), "true"),
		pilotUsers: pilot,
	}, nil
}
