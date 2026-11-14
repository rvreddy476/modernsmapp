package com.us.android.feature.echo.ui

import android.content.Context
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.giphy.sdk.core.models.Media
import com.giphy.sdk.core.models.enums.MediaType
import com.giphy.sdk.core.models.enums.RatingType
import com.giphy.sdk.ui.Giphy
import com.giphy.sdk.ui.pagination.GPHContent
import com.giphy.sdk.ui.views.GPHGridCallback
import com.giphy.sdk.ui.views.GiphyGridView
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.echo.BuildConfig

/**
 * Giphy for the composer's GIF button: the same build-time key as chat.
 * Without a key the button is not drawn and Giphy is never configured.
 */
internal object EchoGiphy {
    private var configured = false

    val available: Boolean get() = BuildConfig.GIPHY_API_KEY.isNotBlank()

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
 * The URL the composer downloads for a picked GIF: Giphy's "downsized"
 * rendition (kept under about 2 MB by Giphy), else the original.
 */
private fun Media.downloadUrl(): String? =
    (images.downsized?.gifUrl ?: images.original?.gifUrl)?.takeIf { it.isNotBlank() }

/**
 * X's GIF picker: a search field over Giphy's grid (trending when empty,
 * rating G), "Powered by GIPHY" under it. A pick closes the sheet.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun GifSheet(onPick: (url: String) -> Unit, onDismiss: () -> Unit) {
    val pick by rememberUpdatedState(onPick)
    val context = LocalContext.current
    var query by remember { mutableStateOf("") }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCanvas,
        modifier = Modifier.testTag("echo_gif_sheet"),
    ) {
        if (!EchoGiphy.ensure(context)) {
            Text(
                "GIFs aren't available right now.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier.padding(UsTheme.spacing.xxl),
            )
            return@ModalBottomSheet
        }
        Column(modifier = Modifier.fillMaxWidth()) {
            UsTextField(
                value = query,
                onValueChange = { query = it },
                label = "Search GIFs",
                modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.xxl),
            )
            Box(modifier = Modifier.fillMaxWidth().height(GRID_HEIGHT).padding(top = UsTheme.spacing.m)) {
                AndroidView(
                    factory = { ctx ->
                        GiphyGridView(ctx).apply {
                            spanCount = COLUMNS
                            cellPadding = CELL_PADDING
                            callback = object : GPHGridCallback {
                                override fun contentDidUpdate(resultCount: Int) = Unit
                                override fun didSelectMedia(media: Media) {
                                    media.downloadUrl()?.let { pick(it) }
                                }
                            }
                        }
                    },
                    update = { grid ->
                        val needle = query.trim()
                        grid.content = if (needle.isEmpty()) {
                            GPHContent.trending(MediaType.gif, RatingType.g)
                        } else {
                            GPHContent.searchQuery(needle, MediaType.gif, RatingType.g)
                        }
                    },
                    modifier = Modifier.fillMaxSize(),
                )
            }
            Text(
                text = "Powered by GIPHY",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier
                    .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
                    .semantics { contentDescription = "Powered by GIPHY" },
            )
        }
    }
}

private const val COLUMNS = 2
private const val CELL_PADDING = 6
private val GRID_HEIGHT = 420.dp
