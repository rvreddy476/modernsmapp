package http

import "strings"

// Request field aliases for the shipped web client (audit M10, 30 Oct 2026).
// postbook-ui's useQA.ts sends {target_user_id} to request an answer and
// {response: "accept"|"decline"} to answer a request; the server reads
// requested_user_id and status ("accepted"|"declined"). Both spellings are
// accepted; the canonical one wins when both are sent. Anything else is
// still refused by the service with 400 (never a database error).

// firstNonEmpty returns the canonical value, else the alias.
func firstNonEmpty(canonical, alias string) string {
	if strings.TrimSpace(canonical) != "" {
		return canonical
	}
	return alias
}

// answerRequestStatus maps status (or the web's response) to the stored
// status: accept/accepted -> accepted, decline/declined -> declined. Any
// other value is passed through for the service to refuse.
func answerRequestStatus(status, response string) string {
	v := strings.ToLower(strings.TrimSpace(firstNonEmpty(status, response)))
	switch v {
	case "accept", "accepted":
		return "accepted"
	case "decline", "declined":
		return "declined"
	}
	return v
}
