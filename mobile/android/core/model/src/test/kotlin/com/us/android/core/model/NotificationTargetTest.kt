package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Parsing the server's deep link — Slice D.
 *
 * ## WHY THIS IS PARSED AND NOT OBEYED
 *
 * The server sends a path string: `/post/{id}?focusComment={id}`, `/u/{id}`.
 * The app's routes are type-safe Kotlin objects, so handing a raw server string
 * to the navigator would make any string the backend ever emits a navigation
 * instruction — including one from a vertical this build has no screen for.
 *
 * So it is parsed into a closed set, and anything unrecognised becomes
 * [NotificationTarget.None]: a row that renders and does nothing when tapped,
 * rather than a crash or a navigation to somewhere nobody intended.
 *
 * The literals below are copied from
 * `notification-service/internal/events/consumer.go` and confirmed against a
 * live notification captured on 2026-08-22.
 */
class NotificationTargetTest {

    @Test
    fun `a comment notification targets the post and carries the comment id`() {
        val target = NotificationTarget.parse(
            "/post/1acdc102-06bf-4047-b515-9acde1f95e8c?focusComment=9220f747-0dc8-4eb5-ac1e-6400668dd1fd",
        )

        assertThat(target).isEqualTo(
            NotificationTarget.PostComment(
                postId = "1acdc102-06bf-4047-b515-9acde1f95e8c",
                commentId = "9220f747-0dc8-4eb5-ac1e-6400668dd1fd",
            ),
        )
    }

    @Test
    fun `a reaction notification targets the post`() {
        assertThat(NotificationTarget.parse("/post/abc"))
            .isEqualTo(NotificationTarget.Post("abc"))
    }

    @Test
    fun `a follow notification targets the profile`() {
        assertThat(NotificationTarget.parse("/u/user-1"))
            .isEqualTo(NotificationTarget.Profile("user-1"))
    }

    /**
     * A post link whose focusComment is present but empty is a POST link.
     *
     * `?focusComment=` with nothing after it would otherwise produce a
     * PostComment carrying an empty id, and the app would try to focus a
     * comment that cannot exist.
     */
    @Test
    fun `an empty focusComment degrades to the post`() {
        assertThat(NotificationTarget.parse("/post/abc?focusComment="))
            .isEqualTo(NotificationTarget.Post("abc"))
    }

    /** Query parameters this build does not know are ignored, not fatal. */
    @Test
    fun `unknown query parameters are ignored`() {
        assertThat(NotificationTarget.parse("/post/abc?utm=x&focusComment=c1"))
            .isEqualTo(NotificationTarget.PostComment("abc", "c1"))
    }

    /**
     * Everything else is [NotificationTarget.None].
     *
     * These are real shapes the server emits — `/live/{id}`, `/page/{id}` —
     * for verticals this build has no screen for, plus malformed input. All of
     * them must render a tappable-but-inert row rather than navigating
     * somewhere approximate.
     */
    @Test
    fun `unknown and malformed links resolve to no target`() {
        val unroutable = listOf(
            "",
            "   ",
            "/",
            "/post",
            "/post/",
            "/u",
            "/live/stream-1",
            "/page/page-1",
            "/order/123",
            "https://evil.example.com/post/abc",
            "post/abc",
        )

        for (link in unroutable) {
            assertThat(NotificationTarget.parse(link)).isEqualTo(NotificationTarget.None)
        }
    }

    /**
     * An absolute URL never resolves.
     *
     * The server sends paths. If one ever sent a full URL — through a bug or a
     * compromised producer — treating it as a route would be the first step of
     * an open-redirect. It resolves to None, asserted above and again here
     * because the reason matters more than the case.
     */
    @Test
    fun `an absolute url is not a navigation instruction`() {
        assertThat(NotificationTarget.parse("https://example.com/u/victim"))
            .isEqualTo(NotificationTarget.None)
    }

    // ── Kind mapping ────────────────────────────────────────────────────

    @Test
    fun `every wire type this app renders maps to a known kind`() {
        val expected = mapOf(
            "reaction" to NotificationKind.Reaction,
            "comment" to NotificationKind.Comment,
            "comment_reaction" to NotificationKind.CommentReaction,
            "follow" to NotificationKind.Follow,
            "mention" to NotificationKind.Mention,
            "post_reposted" to NotificationKind.Repost,
            "friend_request" to NotificationKind.ConnectionRequest,
            "friend_accepted" to NotificationKind.ConnectionAccepted,
            "new_subscriber" to NotificationKind.NewSubscriber,
        )

        for ((wire, kind) in expected) {
            assertThat(NotificationKind.fromWire(wire)).isEqualTo(kind)
        }
    }

    /**
     * An unrecognised type keeps its raw value instead of being dropped.
     *
     * One notification service serves every vertical in this super-app, so this
     * client WILL receive types it has no screen for. A row it cannot explain
     * is still better than a gap in the list, and the raw string is what makes
     * the gap diagnosable.
     */
    @Test
    fun `an unknown type is preserved rather than discarded`() {
        assertThat(NotificationKind.fromWire("commerce.order.shipped"))
            .isEqualTo(NotificationKind.Unknown("commerce.order.shipped"))
    }
}
