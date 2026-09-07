package ranking

import "math"

// Recommending by what a video IS, not only by who made it.
//
// Author affinity answers "show me more from people I already watch". It
// cannot answer "show me more videos like this one" when the video is by
// someone the viewer has never seen — and that is most of the catalogue
// for most viewers, so a feed built on author affinity alone becomes a
// feed of the same dozen people. The topical term is the other half.
//
// HOW STRONG IS THIS, HONESTLY
//
// As strong as the catalogue's metadata, and no stronger. A topic token
// is `cat:<category>` from the post's taxonomy id or `tag:<hashtag>` from
// its hashtags and tags — the only subject information a post carries.
// There is no transcript, no thumbnail model, no embedding: two videos
// about the same thing that were tagged differently look unrelated here,
// and a post with no category and no hashtags is invisible to this term
// entirely. It degrades to exactly nothing in that case, which is the
// right failure — but it means the term's usefulness is a function of how
// often creators fill the category and tag fields, not of this code.
//
// On the current dataset that is rare: a small minority of posts carry a
// category, fewer carry hashtags, and none carry both consistently. So
// this signal is real, correct, and mostly dormant today. It becomes
// worth its 0.20 weight when the composer starts insisting on a category
// for long uploads; until then it is scaffolding with a live wire in it,
// not a working recommender.

// TopicWeight is the topical term's maximum contribution to a score.
//
// Sized against its neighbours rather than picked: quality_boost caps at
// 0.25, momentum at 0.30, social proximity at 0.20, and the interest term
// spans roughly 0.3 to 1.5. At 0.20 a strongly-liked topic is worth about
// as much as a top-quality score — enough to lift a stranger's video past
// a followed account's mediocre one, not enough to outrank the interest
// term on its own. Given how sparse topic metadata currently is, erring
// larger would mean the handful of tagged posts swamping everything else.
const TopicWeight = 0.20

// SeedWeight is the related-videos relatedness term's maximum
// contribution. Larger than TopicWeight because on that surface
// relatedness IS the question being asked: the viewer has told us exactly
// what they want more of by playing it.
const SeedWeight = 0.35

// seedAuthorShare / seedTopicShare split relatedness between "same
// creator" and "same subject".
//
// Weighted towards the creator deliberately, and for a data reason rather
// than a taste one: the author of a post is always known and always
// correct, whereas its topics are present on a minority of posts and are
// free text when they are. A 50/50 split would give the more reliable
// half of the signal the same say as the half that is usually missing.
const (
	seedAuthorShare = 0.6
	seedTopicShare  = 0.4
)

// neutralTopicAffinity mirrors the cold-start interest floor the affinity
// writer squashes around (analytics-service personalization.ColdStartInterest).
// A token scored exactly here carries no information, and the writer does
// not even store those — but a value read from a stale key might be, so
// the normalisation is anchored on it rather than on zero.
const neutralTopicAffinity = 0.3

// normalizeTopicAffinity maps a stored [0,1] affinity onto [-1, 1] centred
// on the neutral point: 0.3 → 0, 1.0 → +1, 0.0 → -1. The two sides are
// scaled by different denominators because the neutral point is not the
// midpoint of the range — treating it as though it were would make every
// mild dislike look like a strong one.
func normalizeTopicAffinity(a float64) float64 {
	if a >= neutralTopicAffinity {
		return (a - neutralTopicAffinity) / (1 - neutralTopicAffinity)
	}
	return (a - neutralTopicAffinity) / neutralTopicAffinity
}

// TopicInterest is the viewer's feeling about a candidate's subject, in
// [-1, 1]. Zero when the post carries no topics, when the viewer has no
// topic history, or when neither overlaps the other — which is the common
// case today and must cost nothing.
//
// The STRONGEST token wins rather than the average. A video tagged
// #guitar #tutorial #2026 by someone who loves guitar should read as a
// guitar video; averaging in two tokens they have no opinion about would
// dilute the one that matters down to a third of its strength. Strongest
// by absolute value, so a single strongly-disliked topic is not hidden
// behind several liked ones — a video about a subject the viewer has
// actively rejected should not be rescued by an incidental tag.
func TopicInterest(topics []string, affinity map[string]float64) float64 {
	if len(topics) == 0 || len(affinity) == 0 {
		return 0
	}
	best := 0.0
	for _, t := range topics {
		a, ok := affinity[t]
		if !ok {
			continue
		}
		n := normalizeTopicAffinity(a)
		if math.Abs(n) > math.Abs(best) {
			best = n
		}
	}
	return clamp(best, -1, 1)
}

// SeedRelatedness scores a candidate against the video the viewer is
// watching now, in [0, 1]. Only the related-videos path sets a seed, so
// this is zero everywhere else.
//
// The topic half is Jaccard overlap rather than a plain intersection
// count: two videos that share one tag out of two are more alike than two
// that share one out of twenty, and a raw count cannot tell them apart.
func SeedRelatedness(seed *Seed, candidateAuthor string, candidateTopics []string) float64 {
	if seed == nil {
		return 0
	}
	related := 0.0
	if candidateAuthor != "" && seed.AuthorID.String() == candidateAuthor {
		related += seedAuthorShare
	}
	related += seedTopicShare * jaccard(seed.Topics, candidateTopics)
	return clamp(related, 0, 1)
}

// jaccard is |A ∩ B| / |A ∪ B| over two small token slices. Either side
// empty is zero overlap — not one, which is what the set-theoretic
// convention for two empty sets would give and which would make every
// untagged post maximally related to every other untagged post.
func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inA := make(map[string]struct{}, len(a))
	for _, t := range a {
		inA[t] = struct{}{}
	}
	union := len(inA)
	inter := 0
	seen := make(map[string]struct{}, len(b))
	for _, t := range b {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, ok := inA[t]; ok {
			inter++
		} else {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
