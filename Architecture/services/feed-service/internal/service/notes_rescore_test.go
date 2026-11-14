package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func item(author uuid.UUID, score float64, source string, age time.Duration, now time.Time) FeedItem {
	return FeedItem{PostID: uuid.New(), AuthorID: author, Score: score, Source: source,
		CreatedAt: now.Add(-age), ContentType: NoteContentType}
}

func TestConversationScore_RepliesOutweighLikes(t *testing.T) {
	replies := noteSignals{Replies: 2, Views: 100}.conversationScore()
	likes := noteSignals{Likes: 10, Views: 100}.conversationScore()
	if replies <= likes {
		t.Fatalf("2 replies scored %.3f, 10 likes %.3f; X weighs a reply ~7 likes", replies, likes)
	}
	if got := (noteSignals{Likes: 1, Views: 1}).conversationScore(); got > 0.05 {
		t.Fatalf("1 like on 1 view scored %.3f; the view prior should keep it small", got)
	}
	if got := (noteSignals{Replies: 1000}).conversationScore(); got != 1 {
		t.Fatalf("score not capped at 1: %.3f", got)
	}
	if got := (noteSignals{Views: -5}).conversationScore(); got != 0 {
		t.Fatalf("negative views gave %.3f", got)
	}
}

func TestRescore_ConversationLiftsAQuieterRankerScore(t *testing.T) {
	now := time.Now()
	talked := item(uuid.New(), 0.5, sourceTimeline, 10*time.Hour, now)
	liked := item(uuid.New(), 0.6, sourceTimeline, 10*time.Hour, now)
	out := rescoreForYou([]FeedItem{liked, talked}, map[uuid.UUID]noteSignals{
		talked.PostID: {Replies: 6, Quotes: 2, Views: 80},
		liked.PostID:  {Likes: 8, Views: 80},
	}, nil, now)
	if out[0].PostID != talked.PostID {
		t.Fatalf("the note people replied to and quoted is not first: %+v", out)
	}
}

func TestRescore_OutOfNetworkDiscount(t *testing.T) {
	now := time.Now()
	following := item(uuid.New(), 1.0, sourceTimeline, time.Hour*30, now)
	rising := item(uuid.New(), 1.0, sourceColdStart, time.Hour*30, now)
	tagged := item(uuid.New(), 1.0, sourceEchoHashtag, time.Hour*30, now)
	out := rescoreForYou([]FeedItem{rising, tagged, following}, nil, nil, now)
	if out[0].PostID != following.PostID || out[1].PostID != tagged.PostID || out[2].PostID != rising.PostID {
		t.Fatalf("order %v %v %v; want following, hashtag, rising", out[0].Source, out[1].Source, out[2].Source)
	}
	if math.Abs(out[2].Score-echoOONFactor) > 1e-9 {
		t.Fatalf("rising note scored %.3f, want %.3f", out[2].Score, echoOONFactor)
	}
}

func TestRescore_AlreadyShownIsDemotedNotDropped(t *testing.T) {
	now := time.Now()
	shown := item(uuid.New(), 1.0, sourceTimeline, 30*time.Hour, now)
	fresh := item(uuid.New(), 0.5, sourceTimeline, 30*time.Hour, now)
	out := rescoreForYou([]FeedItem{shown, fresh}, nil, map[uuid.UUID]bool{shown.PostID: true}, now)
	if len(out) != 2 || out[0].PostID != fresh.PostID {
		t.Fatalf("an already-shown note still leads, or was dropped: %+v", out)
	}
}

func TestRescore_AuthorDecayDownToFloor(t *testing.T) {
	now := time.Now()
	a := uuid.New()
	var pool []FeedItem
	for i := 0; i < 5; i++ {
		pool = append(pool, item(a, 1.0, sourceTimeline, 30*time.Hour+time.Duration(i)*time.Minute, now))
	}
	other := item(uuid.New(), 0.6, sourceTimeline, 30*time.Hour, now)
	out := rescoreForYou(append(pool, other), nil, nil, now)
	if out[2].PostID != other.PostID { // after the author's 1.0 and decayed 0.625
		t.Fatalf("a second author does not break up one author's run: %v", out[1].AuthorID)
	}
	want := []float64{1, 0.625, 0.4375, 0.34375, 0.296875}
	got := []float64{}
	for _, c := range out {
		if c.AuthorID == a {
			got = append(got, c.Score)
		}
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("decay %v, want %v", got, want)
		}
	}
}

func TestRescore_FreshLiftOnlyForYoungUnseenNotes(t *testing.T) {
	now := time.Now()
	young := item(uuid.New(), 0.5, sourceTimeline, time.Hour, now)
	old := item(uuid.New(), 0.5, sourceTimeline, 30*time.Hour, now)
	seen := item(uuid.New(), 0.5, sourceTimeline, time.Hour, now)
	out := rescoreForYou([]FeedItem{old, seen, young}, map[uuid.UUID]noteSignals{
		young.PostID: {}, old.PostID: {}, seen.PostID: {Views: 500},
	}, nil, now)
	scores := map[uuid.UUID]float64{}
	for _, c := range out {
		scores[c.PostID] = c.Score
	}
	if math.Abs(scores[young.PostID]-(0.5+echoFreshLift)) > 1e-9 || scores[old.PostID] != 0.5 || scores[seen.PostID] != 0.5 {
		t.Fatalf("fresh lift misapplied: young %.3f old %.3f seen %.3f", scores[young.PostID], scores[old.PostID], scores[seen.PostID])
	}
}

func TestScaleScore_NeverLiftsANegativeScore(t *testing.T) {
	if got := scaleScore(-0.4, echoOONFactor); got >= -0.4 {
		t.Fatalf("discount raised a penalised score to %.3f", got)
	}
}

func TestServedNotes_RecordAndReadWithinWindow(t *testing.T) {
	ctx := context.Background()
	s := &Service{rdb: redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})}
	viewer := uuid.New()
	now := time.Now()
	old := FeedItem{PostID: uuid.New()}
	recent := FeedItem{PostID: uuid.New()}
	s.recordServedNotes(ctx, viewer, []FeedItem{old}, now.Add(-50*time.Hour))
	s.recordServedNotes(ctx, viewer, []FeedItem{recent}, now)
	got := s.servedNotes(ctx, viewer, now)
	if !got[recent.PostID] || got[old.PostID] {
		t.Fatalf("served set %v; want only the note served inside 48 h", got)
	}
	if other := s.servedNotes(ctx, uuid.New(), now); len(other) != 0 {
		t.Fatalf("another viewer sees %d served notes", len(other))
	}
}

func TestServedNotes_NoRedisIsHarmless(t *testing.T) {
	s := &Service{}
	s.recordServedNotes(context.Background(), uuid.New(), []FeedItem{{PostID: uuid.New()}}, time.Now())
	if got := s.servedNotes(context.Background(), uuid.New(), time.Now()); len(got) != 0 {
		t.Fatalf("got %v without Redis", got)
	}
}
