package service

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Pure decisions behind the Q&A guards (audit 2026-10-30: H2, H5, M4). They
// take what the store read and answer a refusal in the prefixed form the
// HTTP layer maps ("forbidden: ..." 403, "not_found: ..." 404, "conflict:
// ..." 409, "invalid: ..." 400).

// questionVisibility is the by-id read rule (H5), the Go twin of
// store.questionVisibleSQL for lists:
//
//   - a deleted question is not found;
//   - a question in a private or invite-only community is refused to anyone
//     but its members;
//   - a question awaiting approval is not found for anyone but its author
//     and its community's moderators.
func questionVisibility(status string, authorID uuid.UUID, viewer *uuid.UUID, inCommunity, restricted bool, role string) error {
	if status == "deleted" {
		return fmt.Errorf("not_found: question not found")
	}
	if inCommunity && restricted && !isCommunityMember(role) {
		return fmt.Errorf("forbidden: community questions are only visible to members")
	}
	if status == "pending_approval" {
		isAuthor := viewer != nil && *viewer == authorID
		if !isAuthor && !(inCommunity && isCommunityModerator(role)) {
			return fmt.Errorf("not_found: question not found")
		}
	}
	return nil
}

// closeDecision: only an open question closes, and only its author or a
// moderator of its community closes it (H2). Platform moderators lock from
// the admin console instead.
func closeDecision(status string, authorID, actor uuid.UUID, inCommunity bool, role string) error {
	if actor != authorID && !(inCommunity && isCommunityModerator(role)) {
		return fmt.Errorf("forbidden: only the question author or a community moderator can close it")
	}
	if status != "open" {
		return fmt.Errorf("conflict: only an open question can be closed")
	}
	return nil
}

// reopenDecision: only a closed question reopens (never a merged or pending
// one). A community moderator may reopen it; its author only if they closed
// it themselves, so an author cannot undo a moderator's close. A platform
// moderator's lock is re-checked by the store (it holds for everyone).
// asModerator tells the store which rule applied.
func reopenDecision(status string, authorID uuid.UUID, closedBy *uuid.UUID, actor uuid.UUID, inCommunity bool, role string) (asModerator bool, err error) {
	moderator := inCommunity && isCommunityModerator(role)
	if actor != authorID && !moderator {
		return false, fmt.Errorf("forbidden: only the question author or a community moderator can reopen it")
	}
	if status != "closed" {
		return false, fmt.Errorf("conflict: only a closed question can be reopened")
	}
	if moderator {
		return true, nil
	}
	if closedBy == nil || *closedBy != authorID {
		return false, fmt.Errorf("conflict: a question closed by a moderator can only be reopened by a moderator")
	}
	return false, nil
}

// approvalDecision: only a moderator of the community approves or rejects
// its pending questions.
func approvalDecision(role string) error {
	if !isCommunityModerator(role) {
		return fmt.Errorf("forbidden: only community moderators can approve or reject questions")
	}
	return nil
}

// answerRequestDecision: a user does not ask themselves, and only an open
// question takes requests (M4). Whether the target can see the question is
// checked with the visibility rule.
func answerRequestDecision(status string, requester, target uuid.UUID) error {
	if requester == target {
		return fmt.Errorf("invalid: you cannot ask yourself to answer")
	}
	if status != "open" {
		return fmt.Errorf("conflict: only an open question takes answer requests")
	}
	return nil
}

// Length caps (M4), in characters. The gateway's body limit is the only
// other bound.
const (
	maxTitleLen        = 300
	maxQuestionBodyLen = 20000
	maxAnswerBodyLen   = 30000
	maxCommentLen      = 2000
	maxTags            = 10
	maxTagLen          = 50
	maxReasonLen       = 500
	maxReportReasonLen = 100
	maxReportDetailLen = 2000
)

func checkLen(field, v string, maxLen int) error {
	if utf8.RuneCountInString(v) > maxLen {
		return fmt.Errorf("invalid: %s must be at most %d characters", field, maxLen)
	}
	return nil
}

func checkQuestionText(title, body string, tags []string) error {
	if err := checkLen("title", title, maxTitleLen); err != nil {
		return err
	}
	if err := checkLen("body", body, maxQuestionBodyLen); err != nil {
		return err
	}
	if len(tags) > maxTags {
		return fmt.Errorf("invalid: at most %d tags", maxTags)
	}
	for _, t := range tags {
		if err := checkLen("tag", strings.TrimSpace(t), maxTagLen); err != nil {
			return err
		}
	}
	return nil
}

func checkAnswerText(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("invalid: answer body is required")
	}
	return checkLen("body", body, maxAnswerBodyLen)
}

func checkCommentText(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("invalid: comment body is required")
	}
	return checkLen("body", body, maxCommentLen)
}

func checkVoteType(v string) error {
	if v != "up" && v != "down" {
		return fmt.Errorf("invalid: vote_type must be 'up' or 'down'")
	}
	return nil
}
