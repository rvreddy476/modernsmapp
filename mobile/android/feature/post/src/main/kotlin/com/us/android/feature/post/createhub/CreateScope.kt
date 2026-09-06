package com.us.android.feature.post.createhub

/**
 * Where the "+" was pressed, and therefore what it offers.
 *
 * The founder's rule (2026-09-06): "only that plus button should change
 * according to the app we are on". Explore and Reels stay in the bottom bar
 * everywhere; the sheet behind the plus is the one thing that varies.
 *
 * ## ONE PLACE, NOT AN `if` IN THE SHEET
 *
 * The offered set is a property of the SCOPE, resolved here and nowhere
 * else. `CreateSheet` renders whatever [surfaces] and [offersLive] say; it
 * never asks where it is. Adding a mini-app with its own create is then one
 * entry in this enum and one call site that names it — not another branch
 * threaded through the sheet's grid, its swatches and its Go Live row.
 *
 * ## THE TWO SCOPES
 *
 *  - [App] is today's full sheet, unchanged: the seven typed tiles and Go
 *    Live, everywhere the shell's own bar is on screen. **This is an
 *    assumption, not the founder's instruction** — they specified Tube
 *    only. If "elsewhere" should narrow too, it narrows HERE.
 *  - [Tube] is the founder's instruction, exactly: "in Tube the plus must
 *    offer exactly three things: post a video, post a reel, go live".
 *    Video first, because Tube is the video app and the plus in it is read
 *    as "post a video".
 */
enum class CreateScope(
    /** The typed tiles the sheet draws, in the order it draws them. */
    val surfaces: List<CreateSurface>,
    /** Whether the Go Live row sits under the grid. */
    val offersLive: Boolean,
) {
    App(surfaces = CreateSurface.entries.toList(), offersLive = true),
    Tube(surfaces = listOf(CreateSurface.Video, CreateSurface.Reel), offersLive = true),
}
