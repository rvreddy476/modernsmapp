package com.us.android.core.feed.data

/**
 * The order of a profile's grid — post-service
 * `GET /v1/posts/by-author/:id?sort=` (T-21, 2026-10-14).
 *
 *  - [LATEST]: pinned first, then newest. The server's default, and the
 *    order the grid always had.
 *  - [POPULAR]: most liked first.
 *  - [OLDEST]: oldest first; pinning does not apply.
 *
 * [wire] is the exact query value; [label] is what the control says.
 */
enum class AuthorPostSort(val wire: String, val label: String) {
    LATEST("latest", "Latest"),
    POPULAR("popular", "Popular"),
    OLDEST("oldest", "Oldest"),
}
