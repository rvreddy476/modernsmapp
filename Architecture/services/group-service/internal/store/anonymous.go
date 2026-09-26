package store

import (
	"encoding/json"

	"github.com/google/uuid"
)

/*
Masking an anonymous post's author, once, where it cannot be forgotten.

There were three places this could have gone and two of them are wrong:

  - In the HANDLERS. Works until someone adds a route. There are already
    several readers of a post — the feed, the single-post read, the pending
    queue, the media list — and each would need to remember. The one that
    forgets is a leak, and it leaks silently.

  - In the SQL projection, selecting the alias as author_id. This breaks
    ownership: DeleteGroupPostV2 compares AuthorID to the actor to decide
    whether you may delete your own post, and it would start comparing an
    alias. Bans and rate limits need the real id too.

  - On the TYPE, here. The row keeps the real author for everything that
    needs it, and anything that serialises a post to a client is masked by
    the type itself. A route added next year is masked for free.

MarshalJSON uses an alias type to avoid recursing into itself. Every other
field is emitted byte-identically to before, which matters because the mobile
app reads this shape and is not being changed.
*/

// groupPostV2Wire is GroupPostV2 without its MarshalJSON method, so the
// marshaller can delegate to the default encoder without recursing.
type groupPostV2Wire GroupPostV2

func (p GroupPostV2) MarshalJSON() ([]byte, error) {
	// reaction_counts is documented as an object. A post read through a path
	// that never attached counts must still say {} rather than null.
	if p.ReactionCounts == nil {
		p.ReactionCounts = map[string]int{}
	}
	if !p.IsAnonymous {
		return json.Marshal(groupPostV2Wire(p))
	}

	masked := groupPostV2Wire(p)
	/*
		The alias replaces the author id rather than blanking it.

		Clients use author_id as a React key and as the key for a profile
		lookup. An empty string risks a request to /v1/users/ with no id, and
		a fixed sentinel like "anonymous" would make every anonymous post in
		the product look like the same person. A UUID is shape-compatible,
		resolves to nobody, and — being per post — links to nothing else.

		AnonAlias is guaranteed non-nil for an anonymous row by the CHECK
		constraint in migration 013, but this is the one place where being
		wrong leaks the author, so it does not assume: a row that somehow has
		no alias is emitted with an empty author rather than a real one.
	*/
	if p.AnonAlias != nil {
		masked.AuthorID = p.AnonAlias.String()
	} else {
		masked.AuthorID = ""
	}
	// Every copy of an anonymous cross-post gets its own alias, but they
	// share one cross_post_group_id: a member of two target groups could
	// match it and know the posts share an author. Not on the wire.
	masked.CrossPostGroupID = nil
	// Never on the wire in either case.
	masked.AnonAlias = nil
	return json.Marshal(masked)
}

type groupPostCommentWire GroupPostComment

/*
A comment on an anonymous post, by that post's own author.

Replying under your own name to your own anonymous post de-anonymises you
completely, and doing it by accident is easy — you are reading a thread and
you answer a question in it. So the server masks it and does not offer the
choice. IsAnonymous on a comment is set by the service when the commenter is
the post's author and the post is anonymous; it is never a client's decision.

The comment carries the POST's alias, so the author reads as the same
pseudonymous person who wrote it, rather than as a second anonymous party.
*/
func (c GroupPostComment) MarshalJSON() ([]byte, error) {
	if !c.IsAnonymous {
		return json.Marshal(groupPostCommentWire(c))
	}
	masked := groupPostCommentWire(c)
	if c.AnonAlias != nil {
		masked.UserID = c.AnonAlias.String()
	} else {
		masked.UserID = ""
	}
	masked.AnonAlias = nil
	return json.Marshal(masked)
}

// NewAnonAlias mints the per-post pseudonym.
//
// A function rather than an inline uuid.New() so the cross-post loop has an
// obvious thing to call per target: generating one alias above the loop and
// reusing it would make every copy of a cross-post linkable, which is the
// failure the loop's test exists to catch.
func NewAnonAlias() *uuid.UUID {
	id := uuid.New()
	return &id
}
