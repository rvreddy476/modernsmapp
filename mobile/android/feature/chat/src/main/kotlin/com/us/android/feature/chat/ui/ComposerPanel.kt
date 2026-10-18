package com.us.android.feature.chat.ui

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import coil3.compose.AsyncImage
import com.giphy.sdk.core.models.Media
import com.giphy.sdk.core.models.enums.MediaType
import com.giphy.sdk.core.models.enums.RatingType
import com.giphy.sdk.ui.Giphy
import com.giphy.sdk.ui.pagination.GPHContent
import com.giphy.sdk.ui.views.GPHGridCallback
import com.giphy.sdk.ui.views.GiphyGridView
import com.us.android.core.chat.data.GifInfo
import com.us.android.core.chat.data.StickerInfo
import com.us.android.core.chat.data.StickerPack
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmojiPanel
import com.us.android.feature.chat.BuildConfig

/**
 * Giphy, configured once and lazily (founder, 2026-10-18): the key is a
 * build-time value, and without one the GIF tab never shows and
 * [Giphy.configure] is never called. Client-side by Giphy's terms — no
 * server proxy, no cache of ours.
 */
internal object GiphySetup {
    private var configured = false

    val available: Boolean get() = BuildConfig.GIPHY_API_KEY.isNotBlank()

    /** True once Giphy (and the Fresco it brings) is ready to draw. */
    val ready: Boolean get() = configured

    @Synchronized
    fun ensure(context: Context): Boolean {
        if (!available) return false
        if (!configured) {
            runCatching { Giphy.configure(context.applicationContext, BuildConfig.GIPHY_API_KEY) }
                .onSuccess { configured = true }
        }
        return configured
    }
}

/**
 * What a Giphy pick is sent as (CONTRACT §2.1 `type: gif`): the SDK's media
 * id, and its ORIGINAL rendition's url and size EXACTLY as returned — never
 * rewritten — plus the still frame. Pure over plain values so it is a test.
 */
fun giphyGifOf(
    id: String,
    url: String?,
    width: Int,
    height: Int,
    stillUrl: String?,
    isSticker: Boolean = false,
): GifInfo? {
    if (id.isBlank() || url.isNullOrBlank()) return null
    return GifInfo(
        provider = GIPHY_PROVIDER,
        id = id,
        url = url,
        previewUrl = null,
        width = width,
        height = height,
        stillUrl = stillUrl?.takeIf { it.isNotBlank() },
        isSticker = isSticker,
    )
}

private fun Media.toGif(isSticker: Boolean): GifInfo? {
    val original = images.original ?: images.fixedWidth
    val still = images.originalStill ?: images.fixedWidthStill
    return giphyGifOf(id, original?.gifUrl, original?.width ?: 0, original?.height ?: 0, still?.gifUrl, isSticker)
}

/**
 * One tab of the composer's panel (fix pass 2). Emoji first; then, when
 * Giphy is configured for this build, Stickers (Giphy's sticker library) and
 * GIF; then one tab per pack of our own catalogue that has stickers in it.
 */
sealed interface PanelTab {
    val label: String
    val tag: String

    data object Emoji : PanelTab {
        override val label = "Emoji"
        override val tag = "emoji"
    }

    data object Stickers : PanelTab {
        override val label = "Stickers"
        override val tag = "stickers"
    }

    data object Gif : PanelTab {
        override val label = "GIF"
        override val tag = "gif"
    }

    data class Pack(val pack: StickerPack) : PanelTab {
        override val label: String get() = pack.name.trim().ifEmpty { "Pack" }
        override val tag: String get() = "pack:${pack.id}"
    }
}

/** The panel's tabs, in order. Pure, so the founder's tab rules are a test. */
fun composerPanelTabs(giphyAvailable: Boolean, packs: List<StickerPack>): List<PanelTab> = buildList {
    add(PanelTab.Emoji)
    if (giphyAvailable) {
        add(PanelTab.Stickers)
        add(PanelTab.Gif)
    }
    packs.filter { it.stickers.isNotEmpty() }.distinctBy { it.id }.forEach { add(PanelTab.Pack(it)) }
}

/**
 * The composer's panel: Emoji; Stickers and GIF from Giphy (only when Giphy
 * is configured for this build); then our own catalogue's packs, each its
 * own tab, only when it has stickers.
 */
@Composable
internal fun ComposerPanel(
    stickerPacks: List<StickerPack>,
    onEmoji: (String) -> Unit,
    onSticker: (StickerInfo) -> Unit,
    onGif: (GifInfo) -> Unit,
) {
    val tabs = composerPanelTabs(GiphySetup.available, stickerPacks)
    var chosen by remember { mutableStateOf<PanelTab>(PanelTab.Emoji) }
    // A pack that left the catalogue falls back to Emoji rather than a blank panel.
    val tab = chosen.takeIf { it in tabs } ?: PanelTab.Emoji
    Column(modifier = Modifier.fillMaxWidth().testTag("composer-panel")) {
        if (tabs.size > 1) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                modifier = Modifier
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xs),
            ) {
                tabs.forEach { option ->
                    val selected = option == tab
                    Text(
                        text = option.label,
                        style = MaterialTheme.typography.labelLarge,
                        fontWeight = if (selected) FontWeight.Bold else FontWeight.Normal,
                        color = if (selected) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
                        maxLines = 1,
                        modifier = Modifier
                            .clip(RoundedCornerShape(UsTheme.radii.full))
                            .background(if (selected) UsTheme.extended.fillStrong else UsTheme.extended.bgCanvas)
                            .clickable { chosen = option }
                            .semantics {
                                role = Role.Tab
                                this.selected = selected
                            }
                            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.xs)
                            .testTag("composer-tab:${option.tag}"),
                    )
                }
            }
        }
        when (tab) {
            PanelTab.Emoji -> UsEmojiPanel(onPick = onEmoji, modifier = Modifier.testTag("emoji-panel"))
            PanelTab.Stickers -> GiphyPanel(stickers = true, onPick = onGif)
            PanelTab.Gif -> GiphyPanel(stickers = false, onPick = onGif)
            is PanelTab.Pack -> StickerGrid(listOf(tab.pack), onSticker)
        }
    }
}

@Composable
private fun StickerGrid(packs: List<StickerPack>, onSticker: (StickerInfo) -> Unit) {
    LazyVerticalGrid(
        columns = GridCells.Fixed(STICKER_COLUMNS),
        modifier = Modifier
            .fillMaxWidth()
            .height(PANEL_HEIGHT)
            .testTag("sticker-grid"),
    ) {
        packs.forEach { pack ->
            items(pack.stickers, key = { pack.id + ":" + it.id }) { sticker ->
                AsyncImage(
                    model = sticker.url,
                    contentDescription = "Sticker",
                    contentScale = ContentScale.Fit,
                    modifier = Modifier
                        .padding(UsTheme.spacing.xs)
                        .aspectRatio(1f)
                        .clickable { onSticker(sticker) }
                        .testTag("sticker:${sticker.id}"),
                )
            }
        }
    }
}

/**
 * Giphy's own grid (rating G), searched from our field; "Powered by GIPHY"
 * under it. [stickers] browses Giphy's sticker library instead of its GIFs
 * (fix pass 2); a sticker pick is sent as a GIF marked `is_sticker`.
 */
@Composable
private fun GiphyPanel(stickers: Boolean, onPick: (GifInfo) -> Unit) {
    val mediaType = if (stickers) MediaType.sticker else MediaType.gif
    // The grid view outlives recompositions; it must call the CURRENT handler.
    val pick by rememberUpdatedState(onPick)
    val context = LocalContext.current
    var query by remember { mutableStateOf("") }
    if (!GiphySetup.ensure(context)) {
        Text(
            if (stickers) "Stickers aren't available right now." else "GIFs aren't available right now.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(UsTheme.spacing.pageHorizontal),
        )
        return
    }
    Column(modifier = Modifier.fillMaxWidth().testTag(if (stickers) "giphy-sticker-panel" else "gif-panel")) {
        UsTextField(
            value = query,
            onValueChange = { query = it },
            label = if (stickers) "Search stickers" else "Search GIFs",
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
        Box(modifier = Modifier.fillMaxWidth().height(PANEL_HEIGHT)) {
            AndroidView(
                factory = { ctx ->
                    GiphyGridView(ctx).apply {
                        spanCount = GIF_COLUMNS
                        cellPadding = GIF_CELL_PADDING
                        callback = object : GPHGridCallback {
                            override fun contentDidUpdate(resultCount: Int) = Unit
                            override fun didSelectMedia(media: Media) {
                                media.toGif(isSticker = stickers)?.let { pick(it) }
                            }
                        }
                    }
                },
                update = { grid ->
                    val needle = query.trim()
                    grid.content = if (needle.isEmpty()) {
                        GPHContent.trending(mediaType, RatingType.g)
                    } else {
                        GPHContent.searchQuery(needle, mediaType, RatingType.g)
                    }
                },
                modifier = Modifier.fillMaxSize(),
            )
        }
        Text(
            text = "Powered by GIPHY",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xs)
                .semantics { contentDescription = "Powered by GIPHY" }
                .testTag("giphy-attribution"),
        )
    }
}

const val GIPHY_PROVIDER = "giphy"
private const val STICKER_COLUMNS = 4
private const val GIF_COLUMNS = 3
private const val GIF_CELL_PADDING = 6
private val PANEL_HEIGHT = 260.dp
