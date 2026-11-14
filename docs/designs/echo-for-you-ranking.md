# Echo For You ranking: lessons from X's feed code

Status: built 14 Nov 2026 (feed-service `internal/service/notes_rescore.go`).
Scope: the Echo **For You** tab only. Home, Reels, Tube and the shared
`internal/ranking` package are unchanged.

## Why

X publishes its For You code (xai-org/x-algorithm). X ranks with a trained
model that predicts ~25 actions per post and adds the predictions up with
fixed weights. Echo has far too little activity to train such a model, so
this change keeps Echo's hand-written formula and adds X's lessons on top,
using the real counts we already have.

## What X does and what Echo now does

| X lesson | Echo now |
|---|---|
| Replies, quotes and DM shares are worth ~7x a like (reply 5, quote 5, repost 1, like 0.7, share 2.1) | **Conversation score.** `0.7·likes + 1·reposts + 5·replies + 5·quotes + 2·bookmarks`, divided by `views + 20`. Dividing by views turns counts into "how often a viewer acts", the closest thing we have to X's predicted probability; the 20 phantom views stop a note with 1 view and 1 like from topping the list. Capped at 1, weighted 0.6. |
| Posts from people you don't follow are multiplied by a factor below 1 | **Out-of-network discount.** Notes from the "liked/reposted by people you follow" and "rising" sources ×0.75, followed-hashtag notes ×0.9; notes from people you follow ×1. |
| Each extra post from the same author decays, down to a floor | **Author decay.** In score order, an author's 2nd note ×(0.75·0.5 + 0.25), 3rd ×(0.75·0.25 + 0.25)…, never below ×0.25. Replaces nothing: the older "max 3 in a row" rule still runs first. |
| Posts already served are filtered | **Already-shown demotion.** Notes served to this viewer in the last 48 h ×0.35. Demoted, not hidden: with few users, hiding them would empty the tab. |
| New authors with few impressions are lifted | **Fresh-note lift.** A note under 24 h old with fewer than 30 views gets +0.1, so a new voice gets seen before the conversation score can judge it. |
| Posts older than 48 h are dropped | **Not adopted.** The rising source already looks back 48 h only. The "liked/reposted by people you follow" and followed-hashtag sources (7 days) bring older notes on purpose: with a small network there are too few fresh notes. Older notes still sink through the ranker's age decay. |
| Predicted "not interested", mute, block and report weigh -31 to -234 | Already covered: blocks, mutes and "not interested" notes are removed, and an author's "not interested" history lowers their score. There is no per-note report signal in the feed yet. |

## Order of work

1. Gather candidates (unchanged) → filter (unchanged).
2. The shared ranker scores and orders the pool (unchanged).
3. Echo rescoring: read counts for the pool from post-service's
   `/internal/notes/batch` (the same call the page hydration makes), then
   apply the conversation score, out-of-network discount, already-shown
   demotion and fresh-note lift, re-sort, then apply author decay and
   re-sort again.
4. Each page served is recorded in Redis `feed:echo:served:<viewer>`
   (sorted set, 48 h) for the next build's demotion.

Failure rules: if the counts cannot be read, the rescoring uses no counts
(discount, demotion and decay still apply). If the shared ranker fails, the
pool stays newest first and no rescoring runs.

## Later (needs launch traffic)

- Train a model on Echo actions, then use X's weights on its predictions.
- A per-note negative signal (reports, "not interested" from others).
- Author-level reach for the new-author lift instead of per-note views.
