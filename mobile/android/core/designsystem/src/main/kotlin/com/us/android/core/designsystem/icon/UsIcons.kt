package com.us.android.core.designsystem.icon

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathBuilder
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

    /** Outline heart. The resting state of a like. */
    val HeartOutline: ImageVector = stroked("HeartOutline") { heart() }

    /** Solid heart. Reacted. */
    val HeartFilled: ImageVector = filled("HeartFilled") { heart() }

    /**
     * A speech bubble, not an envelope.
     *
     * The tail is the whole reason the shape means "someone said this" — a
     * plain rounded rectangle reads as a card.
     */
    val Comment: ImageVector = stroked("Comment") {
        moveTo(21f, 11.5f)
        curveTo(21f, 16.2f, 17f, 19.6f, 12f, 19.6f)
        curveTo(10.9f, 19.6f, 9.8f, 19.4f, 8.8f, 19.1f)
        lineTo(4.2f, 20.8f)
        lineTo(5.7f, 16.6f)
        curveTo(4f, 15.2f, 3f, 13.5f, 3f, 11.5f)
        curveTo(3f, 6.8f, 7f, 3.4f, 12f, 3.4f)
        curveTo(17f, 3.4f, 21f, 6.8f, 21f, 11.5f)
        close()
    }

    /**
     * A bell — the notification inbox.
     *
     * Stroked to match the rest of the top-bar set. The clapper is a separate
     * short arc rather than part of the body path: a single closed outline
     * reads as a dome at 24dp, and the gap under the bell is what makes it
     * recognisable at that size.
     */
    val Notifications: ImageVector = stroked("Notifications") {
        moveTo(18f, 8.6f)
        curveTo(18f, 5.3f, 15.3f, 2.6f, 12f, 2.6f)
        curveTo(8.7f, 2.6f, 6f, 5.3f, 6f, 8.6f)
        curveTo(6f, 14.4f, 3.5f, 16.1f, 3.5f, 16.1f)
        lineTo(20.5f, 16.1f)
        curveTo(20.5f, 16.1f, 18f, 14.4f, 18f, 8.6f)
        close()
        moveTo(13.7f, 19.4f)
        curveTo(13.4f, 20.4f, 12.5f, 21f, 11.5f, 21f)
        curveTo(10.5f, 21f, 9.6f, 20.4f, 9.3f, 19.4f)
    }

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
    val BookmarkOutline: ImageVector = stroked("BookmarkOutline") { bookmark() }

    /** Solid bookmark. Saved. */
    val BookmarkFilled: ImageVector = filled("BookmarkFilled") { bookmark() }

    /**
     * Share: a paper plane.
     *
     * Replaced a tray-with-arrow, which was the technically correct "share
     * out" mark and looked like a system affordance rather than something you
     * send to a friend. In a social feed the plane is the one everybody
     * already reads as send, and the crease down the middle is what stops it
     * flattening into a plain triangle at 24dp.
     *
     * It does not collide with [Repost]: that is a closed loop of two arrows
     * and means "put this on my own timeline". This one leaves.
     */
    val Share: ImageVector = stroked("Share") {
        // Outline of the plane.
        moveTo(21.2f, 2.8f)
        lineTo(2.8f, 10.2f)
        lineTo(10.4f, 13.6f)
        lineTo(13.8f, 21.2f)
        close()
        // The crease: the near wing folded back under the body.
        moveTo(21.2f, 2.8f)
        lineTo(10.4f, 13.6f)
    }

    /** Upload. The same tray, arrow reversed into it. */
    val Upload: ImageVector = stroked("Upload") {
        moveTo(12f, 3f)
        verticalLineTo(15.2f)
        moveTo(7.8f, 11f)
        lineTo(12f, 15.2f)
        lineTo(16.2f, 11f)
        tray()
    }

    /** Compose. A plus, nothing more — every other reading is noise. */
    val Create: ImageVector = stroked("Create") {
        moveTo(12f, 4.8f)
        verticalLineTo(19.2f)
        moveTo(4.8f, 12f)
        horizontalLineTo(19.2f)
    }

    /** Overflow. */
    val More: ImageVector = filled("More") {
        dot(12f, 5.2f)
        dot(12f, 12f)
        dot(12f, 18.8f)
    }

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
    val Play: ImageVector = filled("Play") {
        moveTo(7.5f, 4.6f)
        lineTo(19.5f, 12f)
        lineTo(7.5f, 19.4f)
        close()
    }

    val Home: ImageVector = stroked("Home") {
        moveTo(3.2f, 10.8f)
        lineTo(12f, 3.4f)
        lineTo(20.8f, 10.8f)
        verticalLineTo(19.6f)
        curveTo(20.8f, 20.4f, 20.2f, 21f, 19.4f, 21f)
        horizontalLineTo(15f)
        verticalLineTo(14.2f)
        horizontalLineTo(9f)
        verticalLineTo(21f)
        horizontalLineTo(4.6f)
        curveTo(3.8f, 21f, 3.2f, 20.4f, 3.2f, 19.6f)
        close()
    }

    /** Two figures — the social graph, not a single account. */
    val Friends: ImageVector = stroked("Friends") {
        circle(9.2f, 8.2f, 3.4f)
        moveTo(2.8f, 20.4f)
        curveTo(2.8f, 16.9f, 5.7f, 14.6f, 9.2f, 14.6f)
        curveTo(12.7f, 14.6f, 15.6f, 16.9f, 15.6f, 20.4f)
        circle(17.4f, 8.8f, 2.6f)
        moveTo(17.2f, 14.8f)
        curveTo(19.7f, 15.2f, 21.4f, 17.3f, 21.4f, 20.4f)
    }

    /** Reels: a frame with a play mark, distinct from the bare [Play]. */
    val Reels: ImageVector = stroked("Reels") {
        moveTo(6.6f, 3.6f)
        horizontalLineTo(17.4f)
        curveTo(19.4f, 3.6f, 21f, 5.2f, 21f, 7.2f)
        verticalLineTo(16.8f)
        curveTo(21f, 18.8f, 19.4f, 20.4f, 17.4f, 20.4f)
        horizontalLineTo(6.6f)
        curveTo(4.6f, 20.4f, 3f, 18.8f, 3f, 16.8f)
        verticalLineTo(7.2f)
        curveTo(3f, 5.2f, 4.6f, 3.6f, 6.6f, 3.6f)
        close()
        moveTo(10.4f, 8.8f)
        lineTo(15.2f, 12f)
        lineTo(10.4f, 15.2f)
        close()
    }

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

    /** Three rising bars — a poll's results, which is what a poll becomes. */
    val Poll: ImageVector = stroked("Poll") {
        moveTo(5f, 21f)
        verticalLineTo(15f)
        moveTo(12f, 21f)
        verticalLineTo(3f)
        moveTo(19f, 21f)
        verticalLineTo(9f)
    }

    /** A serifed capital T — typography, the mark for a text post. */
    val Type: ImageVector = stroked("Type") {
        moveTo(12f, 4f)
        verticalLineTo(20f)
        moveTo(4f, 7f)
        verticalLineTo(5f)
        curveTo(4f, 4.45f, 4.45f, 4f, 5f, 4f)
        horizontalLineTo(19f)
        curveTo(19.55f, 4f, 20f, 4.45f, 20f, 5f)
        verticalLineTo(7f)
        moveTo(9f, 20f)
        horizontalLineTo(15f)
    }

    val Explore: ImageVector = stroked("Explore") {
        circle(10.8f, 10.8f, 6.6f)
        moveTo(15.6f, 15.6f)
        lineTo(21f, 21f)
    }

    val Profile: ImageVector = stroked("Profile") {
        circle(12f, 8f, 3.8f)
        moveTo(4.4f, 20.6f)
        curveTo(4.4f, 16.5f, 7.8f, 13.8f, 12f, 13.8f)
        curveTo(16.2f, 13.8f, 19.6f, 16.5f, 19.6f, 20.6f)
    }

    /** A figure with a plus — invitations and friend requests. */
    val UserPlus: ImageVector = stroked("UserPlus") {
        circle(9f, 8f, 3.6f)
        moveTo(2.6f, 20.6f)
        curveTo(2.6f, 16.7f, 5.6f, 14.2f, 9f, 14.2f)
        curveTo(12.4f, 14.2f, 15.4f, 16.7f, 15.4f, 20.6f)
        moveTo(19f, 7f)
        verticalLineTo(13f)
        moveTo(16f, 10f)
        horizontalLineTo(22f)
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
}

// ---------------------------------------------------------------------------
// Shapes shared between variants.
//
// Kept as functions rather than duplicated path data so an outline and its
// filled twin can never drift — a filled heart a hair wider than the outline
// makes the icon appear to jump at the moment it is tapped.
// ---------------------------------------------------------------------------

private fun PathBuilder.heart() {
    moveTo(12f, 20.6f)
    curveTo(12f, 20.6f, 3f, 14.6f, 3f, 8.9f)
    curveTo(3f, 5.8f, 5.4f, 3.4f, 8.4f, 3.4f)
    curveTo(10.1f, 3.4f, 11.4f, 4.3f, 12f, 5.3f)
    curveTo(12.6f, 4.3f, 13.9f, 3.4f, 15.6f, 3.4f)
    curveTo(18.6f, 3.4f, 21f, 5.8f, 21f, 8.9f)
    curveTo(21f, 14.6f, 12f, 20.6f, 12f, 20.6f)
    close()
}

private fun PathBuilder.bookmark() {
    moveTo(6.4f, 3.2f)
    horizontalLineTo(17.6f)
    curveTo(18.3f, 3.2f, 18.8f, 3.7f, 18.8f, 4.4f)
    verticalLineTo(20.8f)
    lineTo(12f, 16.2f)
    lineTo(5.2f, 20.8f)
    verticalLineTo(4.4f)
    curveTo(5.2f, 3.7f, 5.7f, 3.2f, 6.4f, 3.2f)
    close()
}

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

private fun PathBuilder.dot(cx: Float, cy: Float) = circle(cx, cy, DOT_RADIUS)

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

private fun filled(name: String, block: PathBuilder.() -> Unit): ImageVector =
    ImageVector.Builder(
        name = name,
        defaultWidth = ICON_SIZE.dp,
        defaultHeight = ICON_SIZE.dp,
        viewportWidth = ICON_SIZE,
        viewportHeight = ICON_SIZE,
    ).apply {
        path(fill = SolidColor(Color.Black), pathBuilder = block)
    }.build()

private const val ICON_SIZE = 24f

/**
 * One weight across the whole set. Mixed stroke widths are the most visible
 * sign of an icon set assembled from different sources.
 */
private const val STROKE_WEIGHT = 1.9f

private const val DOT_RADIUS = 1.7f
