package http

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Golden JSON for end screens and cards (2026-09-29), in the same directory
// and under the same UPDATE_CONTRACTS=1 regeneration as TestMTubeContracts,
// which marshals these beside its own. Each is the `data` member.

var (
	fxElementVideo  = uuid.MustParse("c1111111-1111-4111-8111-111111111111")
	fxElementList   = uuid.MustParse("c2222222-2222-4222-8222-222222222222")
	fxElementSub    = uuid.MustParse("c3333333-3333-4333-8333-333333333333")
	fxElementLink   = uuid.MustParse("c4444444-4444-4444-8444-444444444444")
	fxElementLatest = uuid.MustParse("c5555555-5555-4555-8555-555555555555")
	fxElementChan   = uuid.MustParse("c6666666-6666-4666-8666-666666666666")
	fxCardVideo     = uuid.MustParse("d1111111-1111-4111-8111-111111111111")
	fxCardList      = uuid.MustParse("d2222222-2222-4222-8222-222222222222")
	fxCardLink      = uuid.MustParse("d3333333-3333-4333-8333-333333333333")
	fxCardPoll      = uuid.MustParse("d4444444-4444-4444-8444-444444444444")
	fxPollOption    = uuid.MustParse("e1111111-1111-4111-8111-111111111111")
	fxPollOption2   = uuid.MustParse("e2222222-2222-4222-8222-222222222222")
	fxOtherChannel  = uuid.MustParse("f1111111-1111-4111-8111-111111111111")
)

func endScreenContracts() map[string]any {
	avatar := "/v1/media/" + fxBanner.String() + "/serve/avatar"
	linkTitle := "Parts list"
	linkURL := "https://example.com/parts"
	teaser := "The part two build"
	video := &service.EndScreenVideo{ID: fxRelated, Title: "Thursday build", ThumbnailURL: "/v1/media/" + fxCover.String() + "/serve",
		DurationSeconds: 640, ChannelName: "Raghu Builds", ViewCount: 1520}
	playlist := &service.EndScreenPlaylist{ID: fxPlaylist, Title: "Weekend builds", ThumbnailURL: "/v1/media/" + fxCover.String() + "/serve", ItemCount: 7}
	channel := &service.EndScreenChannel{UserID: fxAuthor, Handle: "raghu.builds", Name: "Raghu Builds", AvatarURL: &avatar,
		SubscriberCount: 1200, IsSubscribed: true}
	link := &service.EndScreenLink{URL: linkURL, Title: &linkTitle, Domain: "example.com"}

	viewer := []service.EndScreenElement{
		{ID: fxElementVideo, Type: "video", Position: service.EndScreenPosition{X: 0.05, Y: 0.1, W: 0.3}, StartMs: 705000, EndMs: 725000, Video: video},
		{ID: fxElementList, Type: "playlist", Position: service.EndScreenPosition{X: 0.65, Y: 0.1, W: 0.3}, StartMs: 705000, EndMs: 725000, Playlist: playlist},
		{ID: fxElementSub, Type: "channel_subscribe", Position: service.EndScreenPosition{X: 0.05, Y: 0.5, W: 0.2}, StartMs: 710000, EndMs: 725000, Channel: channel},
		{ID: fxElementLink, Type: "external_link", Position: service.EndScreenPosition{X: 0.65, Y: 0.6, W: 0.3}, StartMs: 715000, EndMs: 725000, Link: link},
	}
	stats := func(imp, clk int64) service.ElementStatsView {
		v := service.ElementStatsView{Impressions: imp, Clicks: clk}
		if imp > 0 {
			v.ClickRate = float64(int64(float64(clk)/float64(imp)*10000+0.5)) / 10000
		}
		return v
	}
	otherChannel := &service.EndScreenChannel{UserID: fxOtherChannel, Handle: "friend.builds", Name: "Friend Builds", SubscriberCount: 88}
	owner := []service.EndScreenOwnerElement{
		{EndScreenElement: viewer[0], VideoMode: "specific", TargetID: &fxRelated, Stats: stats(1200, 54)},
		{EndScreenElement: service.EndScreenElement{ID: fxElementLatest, Type: "video", Position: service.EndScreenPosition{X: 0.65, Y: 0.1, W: 0.3},
			StartMs: 705000, EndMs: 725000, Video: video}, VideoMode: "latest", Stats: stats(0, 0)},
		{EndScreenElement: service.EndScreenElement{ID: fxElementChan, Type: "channel", Position: service.EndScreenPosition{X: 0.05, Y: 0.5, W: 0.2},
			StartMs: 710000, EndMs: 725000, Channel: otherChannel}, VideoMode: "specific", TargetID: &fxOtherChannel, Stats: stats(300, 7)},
		{EndScreenElement: viewer[3], VideoMode: "specific", TargetURL: &linkURL, Title: &linkTitle, Stats: stats(900, 3)},
	}
	request := saveEndScreensRequest{Screens: []endScreenInput{
		{Type: "video", VideoMode: "specific", TargetID: strp(fxRelated.String()), Position: rawPos(0.05, 0.1, 0.3), StartMs: 705000, EndMs: 725000},
		{Type: "video", VideoMode: "latest", Position: rawPos(0.65, 0.1, 0.3), StartMs: 705000, EndMs: 725000},
		{Type: "channel_subscribe", Position: rawPos(0.05, 0.5, 0.2), StartMs: 710000, EndMs: 725000},
		{Type: "external_link", TargetURL: &linkURL, Title: &linkTitle, Position: rawPos(0.65, 0.6, 0.3), StartMs: 715000, EndMs: 725000},
	}}

	cardViewer := []service.VideoCardView{
		{ID: fxCardVideo, Type: "video", AppearAtMs: 83000, Title: "Watch part two", TeaserText: &teaser, Video: video},
		{ID: fxCardList, Type: "playlist", AppearAtMs: 240000, Title: "All the builds", Playlist: playlist},
		{ID: fxCardLink, Type: "external_link", AppearAtMs: 400000, Title: "Parts list", Link: &service.EndScreenLink{URL: linkURL, Title: &linkTitle, Domain: "example.com"}},
	}
	ends := fxTime.Add(48 * time.Hour)
	poll := &service.CardPoll{PostID: fxStream, PollData: &postgres.PollData{Question: "Next build?", EndsAt: &ends, TotalVotes: 30,
		Options: []postgres.PollOption{{ID: fxPollOption, Label: "Kafka", VoteCount: 20, Percentage: 66.67}, {ID: fxPollOption2, Label: "Go", VoteCount: 10, Percentage: 33.33}}}}
	cardOwner := []service.VideoCardOwnerView{
		{VideoCardView: cardViewer[0], TargetID: &fxRelated, Stats: stats(800, 40)},
		{VideoCardView: cardViewer[1], TargetID: &fxPlaylist, Stats: stats(500, 9)},
		{VideoCardView: cardViewer[2], TargetURL: &linkURL, Stats: stats(0, 0)},
		{VideoCardView: service.VideoCardView{ID: fxCardPoll, Type: "poll", AppearAtMs: 600000, Title: "Vote", Poll: poll}, TargetID: &fxStream, Stats: stats(90, 30)},
	}
	cardRequest := saveVideoCardsRequest{Cards: []videoCardInput{
		{Type: "video", TargetID: strp(fxRelated.String()), Title: "Watch part two", TeaserText: &teaser, AppearAtMs: 83000},
		{Type: "playlist", TargetID: strp(fxPlaylist.String()), Title: "All the builds", AppearAtMs: 240000},
		{Type: "external_link", TargetURL: &linkURL, Title: "Parts list", AppearAtMs: 400000},
		{Type: "poll", TargetID: strp(fxStream.String()), Title: "Vote", AppearAtMs: 600000},
	}}
	return map[string]any{
		"end_screens_request.json": request,
		"end_screens_saved.json":   gin.H{"saved": 4},
		"end_screens_viewer.json":  viewer,
		"end_screens_owner.json":   owner,
		"cards_request.json":       cardRequest,
		"cards_saved.json":         gin.H{"saved": 4},
		"cards_viewer.json":        cardViewer,
		"cards_owner.json":         cardOwner,
	}
}

func rawPos(x, y, w float64) json.RawMessage {
	b, _ := json.Marshal(service.EndScreenPosition{X: x, Y: y, W: w})
	return b
}

// The request fixtures decode into the handlers' request structs, and the
// positions into the service's box, so the documented shape is the read one.
func TestEndScreenRequestFixturesDecode(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "mtube")
	var es saveEndScreensRequest
	b, err := os.ReadFile(filepath.Join(dir, "end_screens_request.json"))
	if err != nil || json.Unmarshal(b, &es) != nil || len(es.Screens) != 4 {
		t.Fatalf("end_screens_request.json: %v", err)
	}
	if es.Screens[0].TargetID == nil || es.Screens[1].VideoMode != "latest" || es.Screens[3].TargetURL == nil {
		t.Fatalf("end_screens_request.json fields: %+v", es.Screens)
	}
	if p := service.ParseEndScreenPosition(es.Screens[3].Position, es.Screens[3].Type, 3); p.X != 0.65 || p.Y != 0.6 || p.W != 0.3 {
		t.Fatalf("position: %+v", p)
	}
	var cards saveVideoCardsRequest
	b, err = os.ReadFile(filepath.Join(dir, "cards_request.json"))
	if err != nil || json.Unmarshal(b, &cards) != nil || len(cards.Cards) != 4 || cards.Cards[0].TeaserText == nil {
		t.Fatalf("cards_request.json: %v", err)
	}
}
