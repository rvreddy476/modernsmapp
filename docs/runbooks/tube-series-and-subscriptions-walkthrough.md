# Tube series auto-advance and channel subscriptions: the founder's walkthrough

What shipped on 12 September 2026, what was verified automatically on the development stack, and the numbered steps to see it yourself. Two accounts are needed: **Creator** (owns a Tube channel) and **Viewer**. The dev test channel `@tubetest001` has eighteen long videos and works as Creator.

## Verified automatically on dev (12 Sep)

Through post-service and feed-service with the internal key, viewer id `e0f1143a…`, channel `@tubetest001`; the flow ended unsubscribed, nothing left behind.

| step | observed |
|---|---|
| subscription before | `{subscribed:false}` |
| subscribe | `{status:"subscribed", notify_on:"all", follow:"followed", subscriber_count:1}`; a `follows` row viewer→owner; outbox `tube.channel.subscribed` |
| channel row as viewer | `subscriber_count:1, is_subscribed:true, notify_on:"all"` |
| my subscriptions | `@tubetest001`, `notify_on:"all"` |
| internal subscriber-ids | the viewer, `has_more:false` |
| bell off | `notify_on:"none"`; subscriber-ids now empty |
| bell `highlights` | 400 `INVALID_NOTIFY_ON` |
| feed `subscribed_only=true` | the channel's long video first; `following_only` + `subscribed_only` → 400 |
| unsubscribe | `{status:"unsubscribed", subscriber_count:0}`; follow row gone; PATCH → 404 `NOT_SUBSCRIBED` |
| self-subscribe | 400 `CANNOT_SUBSCRIBE_SELF` |
| series | create; episodes 1, 2, 4; `GET /v1/posts/{ep2}/series` → current 2, next 4, prev 1; last → `next:null`; PATCH private → stranger 404, owner 200; `episode_num` 1000 → 400; delete 204 |

Not verified automatically: a real publish through the studio (needs a signed-in creator), the push on a phone, the countdown in a browser. Those are the steps below.

## Steps

**Web, as Viewer** (`/tube`, signed in):

1. Open `/tube/@tubetest001`. Expect a **Subscribe** button, no bell, and a subscriber count read from the channel.
2. Click **Subscribe**. Expect **Subscribed**, the count up by one, and a bell in the on state. Network: `POST /v1/channels/{ref}/subscribe`. Reload; the state persists.
3. Click the bell. Expect it to turn off (`PATCH …/subscription {"notify_on":"none"}`), label "Notifications off for …". Click again to turn it on.
4. Open `/tube/subscriptions`. Expect the channel's long videos newest first; the request carries `subscribed_only=true`. The left rail lists the channel under Subscriptions.

**Web, as Creator** (`/tube/upload`):

5. Upload a public long video. In **Details**, under **Series**, pick an existing series or create one; the field shows "Will be episode N". Publish. Expect the watch page of the new video to show "In this series".
6. Upload a second episode into the same series.

**Web, as Viewer:**

7. Open episode 1 and let it play to the end. Expect a card "Up next · Episode 2" counting down from 10 with **Play now** and **Cancel**; at zero, episode 2 plays. Press **Escape** during a countdown: it cancels and the theatre does not un-expand.
8. Let the last episode end. Expect an end screen with **Replay** and related videos, and nothing auto-playing.
9. Turn the switch "Autoplay next episode" off (on the card or the series rail). Let an episode end: end screen only. Reload: still off.

**Phone, as Viewer** (install the debug build yourself; nothing here drives the device):

10. Settings → Push notifications: a **New videos** row is present and on.
11. Tube → the channel: **Subscribed** and the bell on, matching the web.
12. Have Creator upload a public long video on the web. Expect a push on the phone's **New videos** channel (long-press the notification to see the channel name) reading "{channel} uploaded: {title}". Tap it: the watch screen opens on that video.
13. The in-app inbox shows "{channel} uploaded a new video"; tapping opens the video.
14. Turn the channel bell off and have Creator upload again: no push, but the video is on the Subscriptions tab.
15. Open an episode from the series and let it end: countdown with Cancel and Play now; the gear sheet has "Autoplay next episode".

**Web, as Viewer:**

16. Unsubscribe. Expect the count down by one, the bell gone, the channel gone from the rail and from `/tube/subscriptions` after reload.
17. Open `/tube/watch/{postId}` directly: the watch page renders (this is the link a push carries).

## What to expect in the database after step 12

```sql
SELECT status, delivered, failed, title, channel_name FROM subscriber_fanout_jobs ORDER BY created_at DESC LIMIT 1;
-- completed | 1 | 0 | <video title> | <channel name>
```

## Known limits, by decision

- No web notification inbox or bell in the top bar; web gets subscribe and the per-channel bell only. Push reaches the phone.
- The home "Following" chip still means follows.
- The Android watch screen's Follow button and reels overlays still follow rather than subscribe.
- Auto-advance never runs outside a series; the last episode stops.
