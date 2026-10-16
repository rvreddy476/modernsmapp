package com.us.android.feature.tube.data.hub

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.tube.ui.hub.HubMetric
import com.us.android.feature.tube.ui.hub.overviewMetrics
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import org.junit.Test

/**
 * T-31: the Creator Hub's normalisers are ports of the web's, and these are
 * the web's own fixtures and expectations (`creatorHubParity.test.tsx`,
 * `hubApi.test.ts`), so the two clients show the same numbers for the same
 * response.
 */
class CreatorHubNormaliseTest {

    private fun json(text: String): JsonElement = Json.parseToJsonElement(text)

    /** The analytics-service creator/me reply, as CreatorStats marshals it (the web's WIRE). */
    private val wire = json(
        """
        {
          "user_id": "u1", "period": "7d",
          "total_views": 90, "total_likes": 14, "total_comments": 5, "total_shares": 3, "follower_growth": 2,
          "top_posts": [],
          "daily_stats": [
            {"date": "2026-10-03T00:00:00Z", "views": 30},
            {"date": "2026-10-01T00:00:00Z", "views": 10}
          ],
          "views": 40, "likes": 14, "comments": 5, "shares": 3, "followers_gained": 2,
          "data_status": "recorded", "watch_time_ms": 660000, "unique_viewers": 21,
          "followers_delta": null,
          "top_content": [{"content_id": "v1", "views": 25, "watch_time_ms": 120000}],
          "realtime": {"views_48h": 4, "series_48h": [1, 3]}
        }
        """,
    )

    // ── creator insights ───────────────────────────────────────────────

    @Test
    fun `maps likes, comments, shares and follows from the wire, and the wire's period`() {
        val d = normalizeCreatorInsights(wire, HubPeriod.DAYS_28)
        assertThat(d.period).isEqualTo("7d")
        assertThat(d.views).isEqualTo(40)
        assertThat(d.likes).isEqualTo(14)
        assertThat(d.comments).isEqualTo(5)
        assertThat(d.shares).isEqualTo(3)
        assertThat(d.followsFromContent).isEqualTo(2)
        assertThat(d.followersDelta).isNull()
        assertThat(d.watchTimeMs).isEqualTo(660_000)
        assertThat(d.uniqueViewers).isEqualTo(21)
    }

    @Test
    fun `daily_stats becomes the daily series - calendar days, oldest first, gaps filled with 0`() {
        val d = normalizeCreatorInsights(wire, HubPeriod.DAYS_7)
        assertThat(d.viewsByDay).containsExactly(
            HubDayPoint("2026-10-01", 10),
            HubDayPoint("2026-10-02", 0),
            HubDayPoint("2026-10-03", 30),
        ).inOrder()
    }

    @Test
    fun `older writers (total_ only, views_by_day) still read - Go zero values fall through`() {
        val d = normalizeCreatorInsights(
            json(
                """{"likes": 0, "total_likes": 7, "total_comments": 2, "total_shares": 1, "follower_growth": 4,
                   "views_by_day": [{"day": "2026-09-01", "views": 3}]}""",
            ),
            HubPeriod.DAYS_28,
        )
        assertThat(d.likes).isEqualTo(7)
        assertThat(d.comments).isEqualTo(2)
        assertThat(d.shares).isEqualTo(1)
        assertThat(d.followsFromContent).isEqualTo(4)
        assertThat(d.viewsByDay).containsExactly(HubDayPoint("2026-09-01", 3))
    }

    @Test
    fun `an empty reply is all zeros, no series, followers not tracked, the requested period`() {
        val d = normalizeCreatorInsights(json("{}"), HubPeriod.DAYS_90)
        assertThat(d.period).isEqualTo("90d")
        val totals = listOf(d.views, d.likes, d.comments, d.shares, d.followsFromContent, d.watchTimeMs)
        assertThat(totals).containsExactly(0L, 0L, 0L, 0L, 0L, 0L)
        assertThat(d.followersDelta).isNull()
        assertThat(d.viewsByDay).isEmpty()
    }

    @Test
    fun `a non-object reply reads as empty`() {
        assertThat(normalizeCreatorInsights(json("[]"), HubPeriod.DAYS_7).views).isEqualTo(0)
        assertThat(normalizeCreatorInsights(null, HubPeriod.DAYS_7).period).isEqualTo("7d")
    }

    @Test
    fun `a period the server does not know keeps the requested one`() {
        assertThat(normalizeCreatorInsights(json("""{"period": "30d"}"""), HubPeriod.DAYS_7).period).isEqualTo("7d")
        assertThat(normalizeCreatorInsights(json("""{"period": "365d"}"""), HubPeriod.DAYS_7).period).isEqualTo("365d")
    }

    @Test
    fun `a followers delta of zero is tracked, and subscribers_delta still reads`() {
        val zero = normalizeCreatorInsights(json("""{"followers_delta": 0}"""), HubPeriod.DAYS_7)
        assertThat(zero.followersDelta).isEqualTo(0)
        val older = normalizeCreatorInsights(json("""{"subscribers_delta": -3}"""), HubPeriod.DAYS_7)
        assertThat(older.followersDelta).isEqualTo(-3)
    }

    @Test
    fun `only JSON numbers are numbers - a quoted count is 0, as on the web`() {
        val d = normalizeCreatorInsights(json("""{"views": "12", "likes": true, "comments": null}"""), HubPeriod.DAYS_7)
        assertThat(d.views).isEqualTo(0)
        assertThat(d.likes).isEqualTo(0)
        assertThat(d.comments).isEqualTo(0)
    }

    @Test
    fun `duplicate days are summed, negative views clamp to 0, a non-ISO day is kept unfilled`() {
        val d = normalizeCreatorInsights(
            json(
                """{"daily_stats": [
                    {"date": "2026-10-02T00:00:00Z", "views": 2},
                    {"date": "2026-10-02 08:00:00", "views": 3},
                    {"date": "2026-10-04T00:00:00Z", "views": -5}
                ]}""",
            ),
            HubPeriod.DAYS_7,
        )
        assertThat(d.viewsByDay).containsExactly(
            HubDayPoint("2026-10-02", 5),
            HubDayPoint("2026-10-03", 0),
            HubDayPoint("2026-10-04", 0),
        ).inOrder()
        assertThat(sortedDays(listOf(HubDayPoint("week 2", 1), HubDayPoint("week 1", 2))))
            .containsExactly(HubDayPoint("week 1", 2), HubDayPoint("week 2", 1)).inOrder()
    }

    @Test
    fun `the analytics body is read bare, and an enveloped one is unwrapped (data, else the body)`() {
        val bare = json("""{"views": 4}""")
        assertThat(unwrapInsightsBody(bare)).isEqualTo(bare)
        val enveloped = unwrapInsightsBody(json("""{"data": {"views": 9}}"""))
        assertThat(normalizeCreatorInsights(enveloped, HubPeriod.DAYS_7).views)
            .isEqualTo(9)
        val nullData = json("""{"data": null, "views": 2}""")
        assertThat(unwrapInsightsBody(nullData)).isEqualTo(nullData)
    }

    // ── overview figures, as the web prints them ───────────────────────

    @Test
    fun `the overview shows views, likes, comments, shares, followers and watch time`() {
        val d = normalizeCreatorInsights(wire, HubPeriod.DAYS_7)
        assertThat(overviewMetrics(d, HubPeriod.DAYS_7)).containsExactly(
            HubMetric("Views", "40", "Last 7 days"),
            HubMetric("Likes", "14", "Last 7 days"),
            HubMetric("Comments", "5", "Last 7 days"),
            HubMetric("Shares", "3", "Last 7 days"),
            HubMetric("Followers", "—", "Not tracked yet · 2 followed from a video"),
            HubMetric("Watch time", "11m", "Time spent watching"),
        ).inOrder()
    }

    @Test
    fun `followers read Not tracked yet when null, and the net change when tracked`() {
        val none = normalizeCreatorInsights(json("{}"), HubPeriod.DAYS_7)
        assertThat(followersNote(none)).isEqualTo("Not tracked yet")
        val tracked = normalizeCreatorInsights(json("""{"followers_delta": 12}"""), HubPeriod.DAYS_7)
        assertThat(formatDelta(tracked.followersDelta)).isEqualTo("+12")
        assertThat(followersNote(tracked)).isEqualTo("Net change in this period")
        assertThat(formatDelta(-3)).isEqualTo("-3")
        assertThat(formatDelta(0)).isEqualTo("0")
    }

    @Test
    fun `counts format exactly as the web's formatCount`() {
        assertThat(hubCount(0)).isEqualTo("0")
        assertThat(hubCount(-4)).isEqualTo("0")
        assertThat(hubCount(999)).isEqualTo("999")
        assertThat(hubCount(1_000)).isEqualTo("1K")
        assertThat(hubCount(1_234)).isEqualTo("1.2K")
        assertThat(hubCount(1_250)).isEqualTo("1.3K")
        assertThat(hubCount(12_345)).isEqualTo("12K")
        assertThat(hubCount(999_999)).isEqualTo("1000K")
        assertThat(hubCount(1_500_000)).isEqualTo("1.5M")
        assertThat(hubCount(2_000_000_000)).isEqualTo("2B")
    }

    @Test
    fun `watch time formats as the web's formatWatchTime`() {
        assertThat(formatWatchTime(660_000)).isEqualTo("11m")
        assertThat(formatWatchTime(3_900_000)).isEqualTo("1h 5m")
        assertThat(formatWatchTime(30_000)).isEqualTo("30s")
        assertThat(formatWatchTime(0)).isEqualTo("0s")
    }

    @Test
    fun `the period labels and notes`() {
        assertThat(HubPeriod.entries.map { it.wire }).containsExactly("7d", "28d", "90d").inOrder()
        assertThat(HubPeriod.entries.map { it.label }).containsExactly("7 days", "28 days", "90 days").inOrder()
        assertThat(HubPeriod.DAYS_90.since).isEqualTo("Last 90 days")
        assertThat(HubPeriod.DEFAULT).isEqualTo(HubPeriod.DAYS_28)
    }

    // ── library rows ───────────────────────────────────────────────────

    @Test
    fun `reads the pinned uploads contract`() {
        val row = normalizeLibraryRow(
            json(
                """{"id": "p1", "title": "A title", "text": "desc\nmore", "content_type": "long_video",
                   "visibility": "unlisted", "scheduled_at": null, "published_at": "2026-09-01T00:00:00Z",
                   "view_count": 12, "comment_count": 3, "processing_status": "ready"}""",
            ),
        )!!
        assertThat(row.title).isEqualTo("A title")
        assertThat(row.visibility).isEqualTo(HubVisibility.UNLISTED)
        assertThat(row.viewCount).isEqualTo(12)
        assertThat(row.commentCount).isEqualTo(3)
        assertThat(row.contentType).isEqualTo("long_video")
        assertThat(row.date).isEqualTo("2026-09-01T00:00:00Z")
    }

    @Test
    fun `tolerates the older row - counts object, empty visibility, title from the text`() {
        val row = normalizeLibraryRow(
            json(
                """{"id": "p2", "text": "first line is the title", "content_type": "long_video",
                   "created_at": "2026-08-01T00:00:00Z", "counts": {"likes": 4, "comments": 9}, "visibility": "",
                   "video_metadata": {"upload_status": "ready"}}""",
            ),
        )!!
        assertThat(row.title).isEqualTo("first line is the title")
        assertThat(row.visibility).isEqualTo(HubVisibility.PUBLIC)
        assertThat(row.commentCount).isEqualTo(9)
        assertThat(row.likeCount).isEqualTo(4)
        assertThat(row.processingStatus).isEqualTo("ready")
        assertThat(row.date).isEqualTo("2026-08-01T00:00:00Z")
    }

    @Test
    fun `the web's uploads contract fixture reads - scheduled, private, the final category`() {
        val row = normalizeLibraryRow(
            json(
                """{"id": "11111111-1111-4111-8111-111111111111", "text": "0:00 Intro\n1:23 Setup",
                   "visibility": "private", "content_type": "long_video", "title": "Friday build",
                   "created_at": "2026-09-27T10:00:00Z", "publish_at": "2026-09-28T12:00:00Z",
                   "counts": {"likes": 0, "comments": 0}, "view_count": 0,
                   "video_metadata": {"final_category": "long_video", "upload_status": "ready"},
                   "scheduled_at": "2026-09-28T12:00:00Z", "comment_count": 0, "processing_status": "ready"}""",
            ),
        )!!
        assertThat(row.title).isEqualTo("Friday build")
        assertThat(row.visibility).isEqualTo(HubVisibility.PRIVATE)
        assertThat(row.scheduledAt).isEqualTo("2026-09-28T12:00:00Z")
        assertThat(row.date).isEqualTo("2026-09-28T12:00:00Z")
    }

    @Test
    fun `like and comment counts - the row's own field wins, a Go zero falls through to counts`() {
        val row = normalizeLibraryRow(
            json(
                """{"id": "v1", "title": "One", "view_count": 9, "like_count": 4, "comment_count": 0,
                   "counts": {"likes": 7, "comments": 2}}""",
            ),
        )!!
        assertThat(row.likeCount).isEqualTo(4)
        assertThat(row.commentCount).isEqualTo(2)
    }

    @Test
    fun `a scheduled_at with no visibility reads as scheduled - unknown values read as public`() {
        assertThat(normalizeVisibility(json("\"\""), "2026-10-01T00:00:00Z")).isEqualTo(HubVisibility.SCHEDULED)
        assertThat(normalizeVisibility(json("\"PUBLIC\""), null)).isEqualTo(HubVisibility.PUBLIC)
        assertThat(normalizeVisibility(json("\"weird\""), null)).isEqualTo(HubVisibility.PUBLIC)
    }

    @Test
    fun `drops rows without an id`() {
        assertThat(normalizeLibraryRow(null)).isNull()
        assertThat(normalizeLibraryRow(json("""{"title": "x"}"""))).isNull()
        assertThat(normalizeLibraryRow(json("""{"id": 7}"""))).isNull()
    }

    @Test
    fun `a row opens where it plays - the web's postHref`() {
        assertThat(hubOpenKind("long_video")).isEqualTo(HubOpenKind.VIDEO)
        assertThat(hubOpenKind("video")).isEqualTo(HubOpenKind.VIDEO)
        assertThat(hubOpenKind("reel")).isEqualTo(HubOpenKind.REEL)
        assertThat(hubOpenKind("FLICK")).isEqualTo(HubOpenKind.REEL)
        assertThat(hubOpenKind("short")).isEqualTo(HubOpenKind.REEL)
        assertThat(hubOpenKind("post")).isEqualTo(HubOpenKind.POST)
        // No content type: the list it came from decides.
        assertThat(hubOpenKind("", HubOpenKind.REEL)).isEqualTo(HubOpenKind.REEL)
    }

    // ── drafts ─────────────────────────────────────────────────────────

    @Test
    fun `drafts - title from the caption's first line, empty ids dropped`() {
        val rows = listOf(
            json(
                """{"id": "d1", "title": "", "caption": "First line
more", "status": "draft",
                   "updated_at": "2026-10-10T00:00:00Z", "media_id": "m1"}""",
            ),
            json("""{"id": "", "title": "dropped"}"""),
            json("""{"id": "d2", "title": "Out", "status": "published", "published_post_id": "p9"}"""),
        ).mapNotNull(::normalizeDraft)
        assertThat(rows.map { it.id }).containsExactly("d1", "d2").inOrder()
        assertThat(rows[0].title).isEqualTo("First line")
        assertThat(rows[1].publishedPostId).isEqualTo("p9")
    }

    @Test
    fun `a draft waiting on its schedule reads as Scheduled, a blocked one as Held`() {
        val scheduled = normalizeDraft(
            json("""{"id": "d3", "status": "draft", "schedule_at": "2026-10-20T10:00:00Z"}"""),
        )!!
        val held = normalizeDraft(json("""{"id": "d5", "status": "BLOCKED", "blocked_reason": "media_missing"}"""))!!
        val bare = normalizeDraft(json("""{"id": "d6", "created_at": "2026-10-01T00:00:00Z"}"""))!!
        assertThat(scheduled.displayStatus).isEqualTo("scheduled")
        assertThat(scheduled.statusLabel).isEqualTo("Scheduled")
        assertThat(held.statusLabel).isEqualTo("Held")
        assertThat(bare.statusLabel).isEqualTo("Draft")
        assertThat(bare.title).isEqualTo("Untitled draft")
        assertThat(bare.updatedAt).isEqualTo("2026-10-01T00:00:00Z")
        assertThat(draftStatusLabel("archived")).isEqualTo("Archived")
    }

    @Test
    fun `the drafts limit is the web's - 20 by default, never above 50`() {
        assertThat(draftsLimit()).isEqualTo(20)
        assertThat(draftsLimit(500)).isEqualTo(50)
        assertThat(draftsLimit(0)).isEqualTo(1)
    }

    // ── comments inbox ─────────────────────────────────────────────────

    @Test
    fun `inbox row - comment, post and author_replied, the body from text`() {
        val r = normalizeInboxRow(
            json(
                """{"comment": {"id": "c1", "post_id": "p1", "author_id": "u1", "text": "hi @you",
                     "created_at": "2026-09-01T00:00:00Z", "author": {"id": "u1", "username": "asha", "display_name": "Asha"}},
                   "post": {"id": "p1", "title": "Video", "content_type": "long_video", "cover_media_id": "cv"},
                   "author_replied": true}""",
            ),
        )!!
        assertThat(r.commentId).isEqualTo("c1")
        assertThat(r.body).isEqualTo("hi @you")
        assertThat(r.authorName).isEqualTo("Asha")
        assertThat(r.postTitle).isEqualTo("Video")
        assertThat(r.postContentType).isEqualTo("long_video")
        assertThat(r.authorReplied).isTrue()
    }

    @Test
    fun `a bare comment row still reads (post from post_id), and the handle stands in for a blank name`() {
        val r = normalizeInboxRow(
            json(
                """{"id": "c2", "post_id": "p9", "author_id": "u", "body": "x",
                   "author": {"username": "kiran", "display_name": " "}}""",
            ),
        )!!
        assertThat(r.postId).isEqualTo("p9")
        assertThat(r.postTitle).isEqualTo("Untitled")
        assertThat(r.authorName).isEqualTo("kiran")
        assertThat(r.authorReplied).isFalse()
    }

    @Test
    fun `an inbox row with no comment id or no post is dropped`() {
        assertThat(normalizeInboxRow(json("""{"comment": {"body": "x"}, "post": {"id": "p1"}}"""))).isNull()
        assertThat(normalizeInboxRow(json("""{"comment": {"id": "c1"}}"""))).isNull()
        assertThat(normalizeInboxRow(json("\"c1\""))).isNull()
    }
}
