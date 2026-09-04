package com.us.android.core.designsystem.icon

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathBuilder
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

/**
 * The product's own icon set.
 *
 * WHY THIS EXISTS
 *
 * The stock Material set does not contain the vocabulary a social feed needs,
 * so using it forces wrong metaphors: an envelope came to mean "comment", a
 * paper plane meant "repost", and a star meant "save". Each one is a different
 * concept from the one it stood in for, and a reader has to learn the mapping
 * instead of recognising it.
 *
 * These are drawn as strokes on a 24x24 grid at a single weight, so a row of
 * them reads as one family rather than a collection of borrowed glyphs. Filled
 * variants exist only where a control has a genuine on state — a like, a
 * bookmark — because fill is what makes that state readable at a glance
 * without relying on colour alone.
 *
 * Kept here and not in a feature module: the same like control appears in the
 * feed, in post detail and over a reel, and three drifting copies of "like" is
 * exactly the inconsistency that reads as an unfinished app.
 */
object UsIcons {

    /** Outline heart, Lucide `heart`. The resting state of a like. */
    val HeartOutline: ImageVector = lucideStroked("HeartOutline", HEART_PATH)

    /** Solid heart, Lucide `heart`. Reacted. */
    val HeartFilled: ImageVector = lucideFilled("HeartFilled", HEART_PATH)

    /**
     * A speech bubble, Lucide `message-circle` — used for both the inline
     * "Comment" action and the Messages tab (they are the same field, so both
     * pick up the swap for free).
     */
    val Comment: ImageVector = lucideStroked(
        "Comment",
        "M2.992 16.342a2 2 0 0 1 .094 1.167l-1.065 3.29a1 1 0 0 0 1.236 1.168l3.413-.998a2 2 0 0 1 " +
            "1.099.092 10 10 0 1 0-4.777-4.719",
    )

    /** A bell, Lucide `bell` — the notification inbox. */
    val Notifications: ImageVector = lucideStroked(
        "Notifications",
        "M10.268 21a2 2 0 0 0 3.464 0",
        "M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 " +
            "4.499-1.411 5.956-2.738 7.326",
    )

    /**
     * Two arrows chasing each other.
     *
     * Deliberately not a paper plane: a plane means "send this to someone",
     * which is share, and share sits in the same row.
     */
    val Repost: ImageVector = stroked("Repost") {
        moveTo(17f, 2.8f)
        lineTo(21.2f, 7f)
        lineTo(17f, 11.2f)
        moveTo(21.2f, 7f)
        horizontalLineTo(7.5f)
        curveTo(5.6f, 7f, 4f, 8.6f, 4f, 10.5f)
        verticalLineTo(13f)
        moveTo(7f, 21.2f)
        lineTo(2.8f, 17f)
        lineTo(7f, 12.8f)
        moveTo(2.8f, 17f)
        horizontalLineTo(16.5f)
        curveTo(18.4f, 17f, 20f, 15.4f, 20f, 13.5f)
        verticalLineTo(11f)
    }

    /** Outline bookmark. The resting state of a save. */
    val BookmarkOutline: ImageVector = lucideStroked("BookmarkOutline", BOOKMARK_PATH)

    /** Solid bookmark, Lucide `bookmark`. Saved. */
    val BookmarkFilled: ImageVector = lucideFilled("BookmarkFilled", BOOKMARK_PATH)

    /**
     * Share: the SOLID curved arrow — the TikTok share mark, which the
     * founder supplied as an image (2026-09-04) after the plain right-arrow,
     * the tray-and-arrow and the paper plane before it. A filled shape, not
     * a stroke: at 24dp the curved tail only reads as an arrow with weight.
     *
     * It does not collide with [Repost]: that is a closed loop of two arrows
     * and means "put this on my own timeline". This one leaves.
     */
    val Share: ImageVector = lucideFilled(
        "Share",
        "M13.4 3.4 L21.7 9.1 C22.4 9.6 22.4 10.4 21.7 10.9 L13.4 16.6 " +
            "C12.9 17 12.4 16.6 12.4 16 V12.2 C8.4 12.5 5.9 15.1 4.6 20.3 " +
            "C4.4 21 3.4 21.1 3.2 20.4 C2.2 15 4.4 8.3 12.4 7.9 V4 " +
            "C12.4 3.4 12.9 3 13.4 3.4 Z",
    )

    /**
     * Send, Lucide `send`: the paper plane. Submits a comment or a live chat
     * line — a message leaving for one place, not [Share]'s "anywhere".
     */
    // A one-time copy of two strings at class init, not a hot path.
    @Suppress("SpreadOperator")
    val Send: ImageVector = lucideStroked("Send", *SEND_PATHS)

    /** Upload. The same tray, arrow reversed into it. */
    val Upload: ImageVector = stroked("Upload") {
        moveTo(12f, 3f)
        verticalLineTo(15.2f)
        moveTo(7.8f, 11f)
        lineTo(12f, 15.2f)
        lineTo(16.2f, 11f)
        tray()
    }

    /** Compose, Lucide `plus`. A plus, nothing more — every other reading is noise. */
    val Create: ImageVector = lucideStroked("Create", "M5 12h14", "M12 5v14")

    /** Overflow, Lucide `ellipsis-vertical`. */
    val More: ImageVector = lucideFilled(
        "More",
        "M12,12 m-1,0 a1,1 0 1,0 2,0 a1,1 0 1,0 -2,0",
        "M12,5 m-1,0 a1,1 0 1,0 2,0 a1,1 0 1,0 -2,0",
        "M12,19 m-1,0 a1,1 0 1,0 2,0 a1,1 0 1,0 -2,0",
    )

    /** Settings sliders: adjustable controls, without borrowing a platform glyph. */
    val Settings: ImageVector = stroked("Settings") {
        moveTo(4f, 6f)
        horizontalLineTo(9f)
        moveTo(15f, 6f)
        horizontalLineTo(20f)
        circle(12f, 6f, 3f)
        moveTo(4f, 18f)
        horizontalLineTo(7f)
        moveTo(13f, 18f)
        horizontalLineTo(20f)
        circle(10f, 18f, 3f)
    }

    /** Muted speaker. Reels open in this state. */
    val SoundOff: ImageVector = stroked("SoundOff") {
        speaker()
        moveTo(16.4f, 9.6f)
        lineTo(21f, 14.4f)
        moveTo(21f, 9.6f)
        lineTo(16.4f, 14.4f)
    }

    /** Speaker with output arcs. */
    val SoundOn: ImageVector = stroked("SoundOn") {
        speaker()
        moveTo(16.2f, 9.4f)
        curveTo(17.4f, 10.6f, 17.4f, 13.4f, 16.2f, 14.6f)
        moveTo(18.8f, 7f)
        curveTo(21.2f, 9.4f, 21.2f, 14.6f, 18.8f, 17f)
    }

    /** Solid play triangle. */
    val Play: ImageVector = lucideFilled(
        "Play",
        "M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z",
    )

    /** Lucide `house` — the Home tab. */
    val Home: ImageVector = lucideStroked(
        "Home",
        "M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8",
        "M3 10a2 2 0 0 1 .709-1.528l7-6a2 2 0 0 1 2.582 0l7 6A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
    )

    /** Two figures, Lucide `users` — the social graph, not a single account. */
    val Friends: ImageVector = lucideStroked(
        "Friends",
        "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2",
        "M16 3.128a4 4 0 0 1 0 7.744",
        "M22 21v-2a4 4 0 0 0-3-3.87",
        "M9,7 m-4,0 a4,4 0 1,0 8,0 a4,4 0 1,0 -8,0",
    )

    /** Lucide `chevron-right` — a row that opens another screen. */
    val ChevronRight: ImageVector = lucideStroked(
        "ChevronRight",
        "m9 18 6-6-6-6",
    )

    /** Reels: the Momentum bottom bar shows the bare [Play] mark for this tab. */
    val Reels: ImageVector get() = Play

    /** A phone handset — the calling vocabulary (missed-call rows, call UI). */
    val Phone: ImageVector = stroked("Phone") {
        moveTo(22f, 16.9f)
        verticalLineTo(19.9f)
        curveTo(22f, 21.1f, 21f, 22.1f, 19.8f, 21.9f)
        curveTo(16.7f, 21.6f, 13.7f, 20.5f, 11.2f, 18.8f)
        curveTo(8.8f, 17.2f, 6.8f, 15.2f, 5.2f, 12.8f)
        curveTo(3.5f, 10.3f, 2.4f, 7.3f, 2.1f, 4.2f)
        curveTo(2f, 3f, 2.9f, 2f, 4.1f, 2f)
        horizontalLineTo(7.1f)
        curveTo(8.1f, 2f, 9f, 2.7f, 9.1f, 3.7f)
        curveTo(9.3f, 4.7f, 9.5f, 5.6f, 9.8f, 6.5f)
        curveTo(10.1f, 7.2f, 9.9f, 8f, 9.4f, 8.6f)
        lineTo(8.1f, 9.9f)
        curveTo(9.6f, 12.4f, 11.6f, 14.4f, 14.1f, 15.9f)
        lineTo(15.4f, 14.6f)
        curveTo(16f, 14.1f, 16.8f, 13.9f, 17.5f, 14.2f)
        curveTo(18.4f, 14.5f, 19.3f, 14.7f, 20.3f, 14.9f)
        curveTo(21.3f, 15f, 22f, 15.9f, 22f, 16.9f)
        close()
    }

    /** A framed photo — sun dot and mountain line, the universal "image". */
    val Photo: ImageVector = stroked("Photo") {
        moveTo(5f, 3f)
        horizontalLineTo(19f)
        curveTo(20.1f, 3f, 21f, 3.9f, 21f, 5f)
        verticalLineTo(19f)
        curveTo(21f, 20.1f, 20.1f, 21f, 19f, 21f)
        horizontalLineTo(5f)
        curveTo(3.9f, 21f, 3f, 20.1f, 3f, 19f)
        verticalLineTo(5f)
        curveTo(3f, 3.9f, 3.9f, 3f, 5f, 3f)
        close()
        circle(9f, 9f, 2f)
        moveTo(21f, 15f)
        lineTo(17.9f, 11.9f)
        curveTo(17.5f, 11.5f, 17f, 11.3f, 16.5f, 11.3f)
        curveTo(16f, 11.3f, 15.5f, 11.5f, 15.1f, 11.9f)
        lineTo(6f, 21f)
    }

    /** A camcorder: body plus the right-facing lens wedge. Video call. */
    val Video: ImageVector = stroked("Video") {
        moveTo(4.8f, 6f)
        horizontalLineTo(13.2f)
        curveTo(14.3f, 6f, 15.2f, 6.9f, 15.2f, 8f)
        verticalLineTo(16f)
        curveTo(15.2f, 17.1f, 14.3f, 18f, 13.2f, 18f)
        horizontalLineTo(4.8f)
        curveTo(3.7f, 18f, 2.8f, 17.1f, 2.8f, 16f)
        verticalLineTo(8f)
        curveTo(2.8f, 6.9f, 3.7f, 6f, 4.8f, 6f)
        close()
        moveTo(15.2f, 10.6f)
        lineTo(21.2f, 7f)
        verticalLineTo(17f)
        lineTo(15.2f, 13.4f)
        close()
    }

    /** A broadcast mark: a dot mid-air between two pairs of waves. Live. */
    val Live: ImageVector = stroked("Live") {
        circle(12f, 12f, 1.8f)
        moveTo(8.6f, 15.4f)
        curveTo(6.8f, 13.5f, 6.8f, 10.5f, 8.6f, 8.6f)
        moveTo(15.4f, 8.6f)
        curveTo(17.2f, 10.5f, 17.2f, 13.5f, 15.4f, 15.4f)
        moveTo(5.8f, 18.2f)
        curveTo(2.4f, 14.8f, 2.4f, 9.2f, 5.8f, 5.8f)
        moveTo(18.2f, 5.8f)
        curveTo(21.6f, 9.2f, 21.6f, 14.8f, 18.2f, 18.2f)
    }

    /** A camera body with its lens — capture, as opposed to [Photo], the library. */
    val Camera: ImageVector = stroked("Camera") {
        moveTo(14.5f, 4f)
        horizontalLineTo(9.5f)
        lineTo(7f, 7f)
        horizontalLineTo(4f)
        curveTo(2.9f, 7f, 2f, 7.9f, 2f, 9f)
        verticalLineTo(18f)
        curveTo(2f, 19.1f, 2.9f, 20f, 4f, 20f)
        horizontalLineTo(20f)
        curveTo(21.1f, 20f, 22f, 19.1f, 22f, 18f)
        verticalLineTo(9f)
        curveTo(22f, 7.9f, 21.1f, 7f, 20f, 7f)
        horizontalLineTo(17f)
        close()
        circle(12f, 13f, 3f)
    }

    /** Lucide `chart-column` — a poll's results, which is what a poll becomes. */
    val Poll: ImageVector = lucideStroked(
        "Poll",
        "M3 3v16a2 2 0 0 0 2 2h16",
        "M18 17V9",
        "M13 17V5",
        "M8 17v-3",
    )

    /** Lucide `type` — the mark for a text post. */
    val Type: ImageVector = lucideStroked(
        "Type",
        "M12 4v16",
        "M4 7V5a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v2",
        "M9 20h6",
    )

    /** Lucide `image` — a photo post, on the Create sheet. */
    val Image: ImageVector = lucideStroked(
        "Image",
        "M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z",
        "M9,9 m-2,0 a2,2 0 1,0 4,0 a2,2 0 1,0 -4,0",
        "m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21",
    )

    /** Lucide `film` — a reel, on the Create sheet. */
    val Film: ImageVector = lucideStroked(
        "Film",
        "M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z",
        "M7 3v18",
        "M3 7.5h4",
        "M3 12h18",
        "M3 16.5h4",
        "M17 3v18",
        "M17 7.5h4",
        "M17 16.5h4",
    )

    /** Lucide `mic` — a voice note, on the Create sheet and the record button. */
    val Mic: ImageVector = lucideStroked(
        "Mic",
        "M12 19v3",
        "M19 10v2a7 7 0 0 1-14 0v-2",
        "M12 2a3 3 0 0 1 3 3v7a3 3 0 0 1-6 0V5a3 3 0 0 1 3-3z",
    )

    /** Lucide `file-text` — a long-form article, on the Create sheet. */
    val FileText: ImageVector = lucideStroked(
        "FileText",
        "M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 " +
            "20 8v12a2 2 0 0 1-2 2z",
        "M14 2v5a1 1 0 0 0 1 1h5",
        "M10 9H8",
        "M16 13H8",
        "M16 17H8",
    )

    /** Lucide `radio` — Go Live, on the Create sheet. */
    val Radio: ImageVector = lucideStroked(
        "Radio",
        "M16.247 7.761a6 6 0 0 1 0 8.478",
        "M19.075 4.933a10 10 0 0 1 0 14.134",
        "M4.925 19.067a10 10 0 0 1 0-14.134",
        "M7.753 16.239a6 6 0 0 1 0-8.478",
        "M12,12 m-2,0 a2,2 0 1,0 4,0 a2,2 0 1,0 -4,0",
    )

    /** Lucide `square-pen` — an edit affordance. */
    val SquarePen: ImageVector = lucideStroked(
        "SquarePen",
        "M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7",
        "M18.375 2.625a1 1 0 0 1 3 3l-9.013 9.014a2 2 0 0 1-.853.505l-2.873.84a.5.5 0 0 1-.62-.62" +
            "l.84-2.873a2 2 0 0 1 .506-.852z",
    )

    /** Lucide `search`. The Explore tab is the app's discover-and-search surface. */
    val Explore: ImageVector = lucideStroked(
        "Explore",
        "m21 21-4.34-4.34",
        "M11,11 m-8,0 a8,8 0 1,0 16,0 a8,8 0 1,0 -16,0",
    )

    /** The header search action — same glyph as [Explore], different call site. */
    val Search: ImageVector get() = Explore

    /** Lucide `circle-user` — the Me tab and profile headers. */
    val Profile: ImageVector = lucideStroked(
        "Profile",
        "M7 20.662V19a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v1.662",
        "M12,12 m-10,0 a10,10 0 1,0 20,0 a10,10 0 1,0 -20,0",
        "M12,10 m-3,0 a3,3 0 1,0 6,0 a3,3 0 1,0 -6,0",
    )

    /** A smiling face — the emoji panel's own face. */
    val Smile: ImageVector = stroked("Smile") {
        circle(12f, 12f, 9f)
        moveTo(8.4f, 14.2f)
        curveTo(9.2f, 15.8f, 10.5f, 16.6f, 12f, 16.6f)
        curveTo(13.5f, 16.6f, 14.8f, 15.8f, 15.6f, 14.2f)
        moveTo(9f, 9.4f)
        lineTo(9f, 9.6f)
        moveTo(15f, 9.4f)
        lineTo(15f, 9.6f)
    }

    /** A figure with a plus — invitations and friend requests. */
    /** Lucide `user-plus` — the follow-requests panel. */
    val UserPlus: ImageVector = lucideStroked(
        "UserPlus",
        "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2",
        "M9,7 m-4,0 a4,4 0 1,0 8,0 a4,4 0 1,0 -8,0",
        "M19,8 L19,14",
        "M22,11 L16,11",
    )

    /** Alias for the follow-requests panel — same glyph as [UserPlus]. */
    val Requests: ImageVector get() = UserPlus

    /** Lucide `map-pin` — the reel form's "Add location" row. */
    val MapPin: ImageVector = lucideStroked(
        "MapPin",
        "M20 10c0 4.993-5.539 10.193-7.399 11.799a1 1 0 0 1-1.202 0C9.539 20.193 4 14.993 4 10a8 8 0 0 1 16 0",
        "M12 13a3 3 0 1 0 0-6 3 3 0 0 0 0 6",
    )

    /** Lucide `globe` — audience rows. */
    val Globe: ImageVector = lucideStroked(
        "Globe",
        "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20",
        "M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20",
        "M2 12h20",
    )

    /** Lucide `tag` — category rows. */
    val Tag: ImageVector = lucideStroked(
        "Tag",
        "M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 " +
            "2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z",
        "M7.5 8a.5.5 0 1 0 0-1 .5.5 0 0 0 0 1",
    )

    /** Lucide `check` — a chosen option. */
    val Check: ImageVector = lucideStroked("Check", "M20 6 9 17l-5-5")

    // ── The post "more" sheet (2026-09-04) ──────────────────────────────

    /** Lucide `link` — copy the post's link. */
    val Link: ImageVector = lucideStroked(
        "Link",
        "M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71",
        "M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71",
    )

    /** Lucide `info` — "Why you're seeing this post". */
    val Info: ImageVector = lucideStroked(
        "Info",
        "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20",
        "M12 16v-4",
        "M12 8h.01",
    )

    /** Lucide `thumbs-up` — Interested. */
    val ThumbsUp: ImageVector = lucideStroked(
        "ThumbsUp",
        "M7 10v12",
        "M15 5.88 14 10h5.83a2 2 0 0 1 1.92 2.56l-2.33 8A2 2 0 0 1 17.5 22H4a2 2 0 0 1-2-2v-8a2 2 0 0 1 " +
            "2-2h2.76a2 2 0 0 0 1.79-1.11L12 2a3.13 3.13 0 0 1 3 3.88Z",
    )

    /** Lucide `thumbs-down` — Not interested. */
    val ThumbsDown: ImageVector = lucideStroked(
        "ThumbsDown",
        "M17 14V2",
        "M9 18.12 10 14H4.17a2 2 0 0 1-1.92-2.56l2.33-8A2 2 0 0 1 6.5 2H20a2 2 0 0 1 2 2v8a2 2 0 0 1-2 " +
            "2h-2.76a2 2 0 0 0-1.79 1.11L12 22a3.13 3.13 0 0 1-3-3.88Z",
    )

    /** Lucide `user-minus` — Unfollow. */
    val UserMinus: ImageVector = lucideStroked(
        "UserMinus",
        "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2",
        "M9,7 m-4,0 a4,4 0 1,0 8,0 a4,4 0 1,0 -8,0",
        "M22 11h-6",
    )

    /** Lucide `ban` — Block. */
    val Ban: ImageVector = lucideStroked(
        "Ban",
        "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20",
        "m4.9 4.9 14.2 14.2",
    )

    /** Lucide `flag` — Report. */
    val Flag: ImageVector = lucideStroked(
        "Flag",
        "M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z",
        "M4 22v-7",
    )

    /** Lucide `chevron-down` — an inline expander, rotated when open. */
    val ChevronDown: ImageVector = lucideStroked("ChevronDown", "m6 9 6 6 6-6")

    /** Lucide `trash-2` — Delete post. */
    val Trash: ImageVector = lucideStroked(
        "Trash",
        "M10 11v6",
        "M14 11v6",
        "M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6",
        "M3 6h18",
        "M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2",
    )

    /** Lucide `rotate-ccw` — Restore a deleted post. */
    val RotateCcw: ImageVector = lucideStroked(
        "RotateCcw",
        "M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8",
        "M3 3v5h5",
    )

    /** A right arrow — the chat send glyph (the design's circular send). */
    val Forward: ImageVector = stroked("Forward") {
        moveTo(4f, 12f)
        horizontalLineTo(19.6f)
        moveTo(13.6f, 5.6f)
        lineTo(20f, 12f)
        lineTo(13.6f, 18.4f)
    }

    val Back: ImageVector = stroked("Back") {
        moveTo(20f, 12f)
        horizontalLineTo(4.4f)
        moveTo(10.4f, 5.6f)
        lineTo(4f, 12f)
        lineTo(10.4f, 18.4f)
    }

    /**
     * A close cross — dismiss a full-screen surface.
     *
     * Distinct from [Back] on purpose. An arrow means "return to where you
     * came from"; a cross means "abandon what is on screen". The composer is
     * the second: leaving it decides the fate of a draft, and offering an
     * arrow there quietly implies the work is being kept.
     */
    val Close: ImageVector = stroked("Close") {
        moveTo(18f, 6f)
        lineTo(6f, 18f)
        moveTo(6f, 6f)
        lineTo(18f, 18f)
    }

    /** A padlock — marks a private account, next to its name. */
    val Lock: ImageVector = stroked("Lock") {
        moveTo(6.5f, 10.5f)
        horizontalLineTo(17.5f)
        curveTo(18.6f, 10.5f, 19.5f, 11.4f, 19.5f, 12.5f)
        verticalLineTo(19f)
        curveTo(19.5f, 20.1f, 18.6f, 21f, 17.5f, 21f)
        horizontalLineTo(6.5f)
        curveTo(5.4f, 21f, 4.5f, 20.1f, 4.5f, 19f)
        verticalLineTo(12.5f)
        curveTo(4.5f, 11.4f, 5.4f, 10.5f, 6.5f, 10.5f)
        close()
        moveTo(8f, 10.5f)
        verticalLineTo(7.5f)
        curveTo(8f, 5.3f, 9.8f, 3.5f, 12f, 3.5f)
        curveTo(14.2f, 3.5f, 16f, 5.3f, 16f, 7.5f)
        verticalLineTo(10.5f)
    }
}

// ---------------------------------------------------------------------------
// Shapes shared between variants.
// ---------------------------------------------------------------------------

private fun PathBuilder.tray() {
    moveTo(4.8f, 12.6f)
    verticalLineTo(18.8f)
    curveTo(4.8f, 20f, 5.8f, 21f, 7f, 21f)
    horizontalLineTo(17f)
    curveTo(18.2f, 21f, 19.2f, 20f, 19.2f, 18.8f)
    verticalLineTo(12.6f)
}

private fun PathBuilder.speaker() {
    moveTo(3.4f, 9.4f)
    horizontalLineTo(6.6f)
    lineTo(11.4f, 5.2f)
    verticalLineTo(18.8f)
    lineTo(6.6f, 14.6f)
    horizontalLineTo(3.4f)
    close()
}

private fun PathBuilder.circle(cx: Float, cy: Float, r: Float) {
    moveTo(cx - r, cy)
    arcToRelative(r, r, 0f, true, true, r * 2, 0f)
    arcToRelative(r, r, 0f, true, true, -r * 2, 0f)
    close()
}

// ---------------------------------------------------------------------------

/**
 * Paths are declared black because [androidx.compose.material3.Icon] replaces
 * the colour with its tint. Baking a real colour in here would make every icon
 * ignore the theme.
 */
private fun stroked(name: String, block: PathBuilder.() -> Unit): ImageVector =
    ImageVector.Builder(
        name = name,
        defaultWidth = ICON_SIZE.dp,
        defaultHeight = ICON_SIZE.dp,
        viewportWidth = ICON_SIZE,
        viewportHeight = ICON_SIZE,
    ).apply {
        path(
            fill = null,
            stroke = SolidColor(Color.Black),
            strokeLineWidth = STROKE_WEIGHT,
            strokeLineCap = StrokeCap.Round,
            strokeLineJoin = StrokeJoin.Round,
            pathBuilder = block,
        )
    }.build()

/**
 * Lucide icons ported straight from their upstream SVG `d` attributes via
 * [addPathNodes] rather than hand-translated into [PathBuilder] calls — the
 * path syntax is the same grammar, so this is a faithful copy, not a redraw.
 * Each string is one `<path>`/`<circle>`/`<line>` element from the source SVG
 * (a circle becomes the usual two-arc path trick).
 *
 * Momentum (Figma YsWb936muw8pwIxgb0je2A) specifies Lucide's own 2px stroke
 * on a 24 viewport, which is why this ignores [STROKE_WEIGHT] — matching the
 * rest of the hand-drawn set here would blur the vocabulary the design
 * actually asked for.
 */
private fun lucideStroked(name: String, vararg pathData: String): ImageVector =
    ImageVector.Builder(
        name = name,
        defaultWidth = ICON_SIZE.dp,
        defaultHeight = ICON_SIZE.dp,
        viewportWidth = ICON_SIZE,
        viewportHeight = ICON_SIZE,
    ).apply {
        pathData.forEach { d ->
            addPath(
                pathData = addPathNodes(d),
                fill = null,
                stroke = SolidColor(Color.Black),
                strokeLineWidth = LUCIDE_STROKE_WEIGHT,
                strokeLineCap = StrokeCap.Round,
                strokeLineJoin = StrokeJoin.Round,
            )
        }
    }.build()

/** The filled twin of [lucideStroked] — same source paths, solid fill. */
private fun lucideFilled(name: String, vararg pathData: String): ImageVector =
    ImageVector.Builder(
        name = name,
        defaultWidth = ICON_SIZE.dp,
        defaultHeight = ICON_SIZE.dp,
        viewportWidth = ICON_SIZE,
        viewportHeight = ICON_SIZE,
    ).apply {
        pathData.forEach { d ->
            addPath(pathData = addPathNodes(d), fill = SolidColor(Color.Black))
        }
    }.build()

/** Lucide `heart`, shared by [UsIcons.HeartOutline] and [UsIcons.HeartFilled]. */
private const val HEART_PATH = "M2 9.5a5.5 5.5 0 0 1 9.591-3.676.56.56 0 0 0 .818 0A5.49 5.49 0 0 1 22 " +
    "9.5c0 2.29-1.5 4-3 5.5l-5.492 5.313a2 2 0 0 1-3 .019L5 15c-1.5-1.5-3-3.2-3-5.5"

/** Lucide `bookmark`, shared by [UsIcons.BookmarkOutline] and [UsIcons.BookmarkFilled]. */
private const val BOOKMARK_PATH = "M17 3a2 2 0 0 1 2 2v15a1 1 0 0 1-1.496.868l-4.512-2.578a2 2 0 0 0-1.984 " +
    "0l-4.512 2.578A1 1 0 0 1 5 20V5a2 2 0 0 1 2-2z"

/** Lucide `send`, the paper plane shared by [UsIcons.Share] and [UsIcons.Send]. */
private val SEND_PATHS = arrayOf(
    "M14.536 21.686a.5.5 0 0 0 .937-.024l6.5-19a.496.496 0 0 0-.635-.635l-19 6.5a.5.5 0 0 0-.024.937" +
        "l7.93 3.18a2 2 0 0 1 1.112 1.11z",
    "m21.854 2.147-10.94 10.939",
)

private const val ICON_SIZE = 24f

/**
 * One weight across the hand-drawn portion of the set. Mixed stroke widths
 * are the most visible sign of an icon set assembled from different sources.
 */
private const val STROKE_WEIGHT = 1.9f

/** Lucide's own stroke weight on a 24 viewport — see [lucideStroked]. */
private const val LUCIDE_STROKE_WEIGHT = 2f
