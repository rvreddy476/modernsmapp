package http

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

func chatCase(rt productRoute) string {
	switch {
	case strings.HasSuffix(rt.operation, ".report.decide"):
		return `{"decision":"uphold","reason":"spam"}`
	case rt.method == http.MethodGet:
		return ""
	}
	return `{"reason":"repeated abuse"}`
}

// chatTables is the three chat products: console prefix, product prefix, table.
var chatTables = []struct {
	name, prefix, productPrefix string
	routes                      []productRoute
}{
	{"channels", chatChannelsPrefix, service.ChannelAdminPrefix, ChatChannelRoutes},
	{"groups", chatGroupsPrefix, service.GroupAdminPrefix, ChatGroupRoutes},
	{"communities", chatCommunityPrefix, service.CommunityAdminPrefix, ChatCommunityRoutes},
}

func TestChatRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(ChatChannelRoutes) != 5 || len(ChatGroupRoutes) != 3 || len(ChatCommunityRoutes) != 3 {
		t.Fatalf("chat tables %d/%d/%d, want 5/3/3", len(ChatChannelRoutes), len(ChatGroupRoutes), len(ChatCommunityRoutes))
	}
	for _, tb := range chatTables {
		for _, rt := range tb.routes {
			t.Run(tb.name+" "+rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
				// Every chat permission but the route's, so a channel moderator
				// cannot decide a group report and the other way round.
				routeTableCase(t, rg, tb.prefix, tb.productPrefix, "chat", "chat", channelAll, rt, chatCase(rt))
			})
		}
	}
}

// Channel suspend and unsuspend need a step-up; report reads and decisions
// do not.
func TestChatRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, tb := range chatTables {
		for _, rt := range tb.routes {
			want := rt.operation == "chat.channel.suspend" || rt.operation == "chat.channel.unsuspend"
			if rt.stepUp != want {
				t.Fatalf("%s declares step-up=%v, want %v", rt.operation, rt.stepUp, want)
			}
			stepUpCase(t, rg, tb.prefix, channelAll, rt, chatCase(rt), want)
		}
	}
}

// Each chat product is reached at its own service: a group report decision
// goes to group-service's prefix, never to channel-service's.
func TestChatRoutes_EachProductAtItsOwnService(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, channelAll...)
	id := uuid.NewString()
	for _, c := range []struct{ path, upstream string }{
		{chatChannelsPrefix + "/reports/" + id, service.ChannelAdminPrefix + "/channel-reports/" + id},
		{chatGroupsPrefix + "/reports/" + id, service.GroupAdminPrefix + "/reports/" + id},
		{chatCommunityPrefix + "/reports/" + id, service.CommunityAdminPrefix + "/reports/" + id},
	} {
		w := rg.do(http.MethodGet, c.path, "", actor, false)
		hits := rg.takeHits()
		rg.takeAudit()
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].path != c.upstream {
			t.Fatalf("%s: %d hits=%+v, want %s", c.path, w.Code, hits, c.upstream)
		}
	}
}

// Chat stats merge the three services; one that fails is unavailable, the
// other two still shown; none answering is 503.
func TestChatStats_MergedAndAFailedSourceIsUnavailableNotZero(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permChatStatsRead)
	for _, p := range []string{service.ChannelAdminPrefix, service.GroupAdminPrefix, service.CommunityAdminPrefix} {
		rg.on(http.MethodGet, p+"/stats", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"open_reports":4}}`))
		})
	}
	w := rg.do(http.MethodGet, chatPrefix+"/stats", "", actor, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 3 {
		t.Fatalf("all up: %d hits=%d %s", w.Code, len(hits), w.Body.String())
	}
	seen := map[string]bool{}
	for _, h := range hits {
		if h.aud != "chat" || h.verifyErr != nil || h.verified.Scope[0] != permChatStatsRead || h.verified.Actor != actor {
			t.Fatalf("hit %+v", h)
		}
		seen[h.path] = true
	}
	if len(seen) != 3 {
		t.Fatalf("stats paths %v, want the three services", seen)
	}
	m := merged(t, w)
	if !m.Complete || len(m.Parts) != 3 {
		t.Fatalf("merged %s", w.Body.String())
	}
	for _, name := range []string{"channels", "groups", "communities"} {
		if m.Parts[name].Status != StatsOK || !strings.Contains(string(m.Parts[name].Stats), `"open_reports":4`) {
			t.Fatalf("%s: %+v", name, m.Parts[name])
		}
	}
	rg.takeAudit()

	// group-service refuses: groups unavailable, the rest shown.
	rg.on(http.MethodGet, service.GroupAdminPrefix+"/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	w = rg.do(http.MethodGet, chatPrefix+"/stats", "", actor, false)
	rg.takeHits()
	m = merged(t, w)
	if w.Code != http.StatusOK || m.Complete || m.Parts["groups"].Status != StatsUnavailable || m.Parts["groups"].UpstreamStatus != 503 ||
		len(m.Parts["groups"].Stats) != 0 || m.Parts["channels"].Status != StatsOK || m.Parts["communities"].Status != StatsOK {
		t.Fatalf("groups down: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"open_reports":0`) {
		t.Fatalf("a failed source was shown as zeros: %s", w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || fmt.Sprint(a[0].Payload["unavailable"]) != "[groups]" {
		t.Fatalf("audit %+v", a)
	}

	// All three down.
	for _, p := range []string{service.ChannelAdminPrefix, service.CommunityAdminPrefix} {
		rg.on(http.MethodGet, p+"/stats", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	}
	w = rg.do(http.MethodGet, chatPrefix+"/stats", "", actor, false)
	rg.takeHits()
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeStatsUnavailable) || merged(t, w).Complete {
		t.Fatalf("all down: %d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure {
		t.Fatalf("audit %+v", a)
	}
}
