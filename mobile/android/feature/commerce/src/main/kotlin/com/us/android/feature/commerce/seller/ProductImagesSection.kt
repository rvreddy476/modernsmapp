package com.us.android.feature.commerce.seller

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.commerce.ui.CommerceProgressLine
import com.us.android.feature.commerce.ui.pressScale

/**
 * The seller's gallery editor.
 *
 * Multi-select up to eight, each with its own upload progress, reorder with
 * the two arrows, delete with the cross, and the first one is the cover —
 * stated on the card rather than left to be inferred, because "which of these
 * do buyers see in the grid" is the question a seller has about a gallery.
 *
 * Used both inside "New product", where the images are attached the moment the
 * listing has an id, and on its own screen for editing an existing product.
 */
@Composable
fun ProductImagesSection(
    state: ProductImagesState,
    onPicked: (List<String>) -> Unit,
    onRemove: (String) -> Unit,
    onMove: (String, Int) -> Unit,
    onMakeCover: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    // The photo picker, not the file picker: a product photo is an image, and
    // the visual-media picker does not need a storage permission on any
    // supported release.
    val pick = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(MAX_PRODUCT_IMAGES),
    ) { uris -> onPicked(uris.map { it.toString() }) }

    Column(
        modifier = modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "Photos",
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "${state.images.size} of $MAX_PRODUCT_IMAGES",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        Text(
            text = "The first photo is the cover buyers see in the grid. " +
                "Use the arrows to reorder.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )

        LazyRow(
            modifier = Modifier.fillMaxWidth().testTag("seller_product_images"),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            contentPadding = PaddingValues(vertical = UsTheme.spacing.xs),
        ) {
            items(state.images, key = { it.key }) { image ->
                ImageCard(
                    image = image,
                    isCover = image.key == state.cover,
                    canMoveEarlier = state.images.indexOf(image) > 0,
                    canMoveLater = state.images.indexOf(image) < state.images.lastIndex,
                    onRemove = { onRemove(image.key) },
                    onMove = { offset -> onMove(image.key, offset) },
                    onMakeCover = { onMakeCover(image.key) },
                )
            }
            if (state.canAddMore) {
                item(key = "add") {
                    AddCard(
                        onClick = {
                            pick.launch(
                                PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                            )
                        },
                    )
                }
            }
        }

        state.notice?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        state.error?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }
    }
}

/**
 * One photo: the picture, its own progress line while bytes move, its own
 * error line if it failed, and the three controls.
 *
 * The failure is on the CARD because eight uploads can be in flight and a
 * single screen-level line cannot say which photo went wrong.
 */
@Composable
private fun ImageCard(
    image: ProductImageDraft,
    isCover: Boolean,
    canMoveEarlier: Boolean,
    canMoveLater: Boolean,
    onRemove: () -> Unit,
    onMove: (Int) -> Unit,
    onMakeCover: () -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Column(
        modifier = Modifier.width(CARD_WIDTH).testTag("seller_image:${image.key}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Box(
            modifier = Modifier
                .size(CARD_WIDTH)
                .clip(shape)
                .background(UsTheme.extended.bgCard)
                .border(
                    width = if (isCover) COVER_BORDER else HAIRLINE,
                    // Selected is WHITE across the whole shop; the accent
                    // belongs to primary actions, not to a state.
                    color = if (isCover) Color.White else UsTheme.extended.borderSubtle,
                    shape = shape,
                ),
        ) {
            val model = image.remoteUrl ?: image.uri.takeIf { it.isNotBlank() }
            if (model != null) {
                AsyncImage(
                    model = model,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            }
            image.progress?.let { progress ->
                CommerceProgressLine(
                    progress = progress,
                    contentDescription = "Uploading photo",
                    modifier = Modifier
                        .align(Alignment.BottomCenter)
                        .padding(UsTheme.spacing.s),
                )
            }
            ImageControl(
                icon = UsIcons.Close,
                description = "Remove this photo",
                onClick = onRemove,
                tag = "seller_image_remove:${image.key}",
                modifier = Modifier.align(Alignment.TopEnd).padding(UsTheme.spacing.xs),
            )
        }

        if (isCover) {
            Text(
                text = "Cover",
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
        }
        image.error?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.statusDanger,
            )
        }

        OrderControls(
            key = image.key,
            isCover = isCover,
            canMoveEarlier = canMoveEarlier,
            canMoveLater = canMoveLater,
            onMove = onMove,
            onMakeCover = onMakeCover,
        )
    }
}

/**
 * The reorder row under a photo.
 *
 * Arrows rather than a drag handle: a drag inside a horizontally scrolling
 * rail fights the rail for the same gesture, and an arrow is the only one of
 * the two a screen reader can operate at all. "Make cover" is offered only
 * where it would change something.
 */
@Composable
private fun OrderControls(
    key: String,
    isCover: Boolean,
    canMoveEarlier: Boolean,
    canMoveLater: Boolean,
    onMove: (Int) -> Unit,
    onMakeCover: () -> Unit,
) {
    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        if (canMoveEarlier) {
            ImageControl(
                icon = UsIcons.Back,
                description = "Move this photo earlier",
                onClick = { onMove(-1) },
                tag = "seller_image_earlier:$key",
            )
            if (!isCover) {
                ImageControl(
                    icon = UsIcons.Check,
                    description = "Make this the cover",
                    onClick = onMakeCover,
                    tag = "seller_image_cover:$key",
                )
            }
        }
        if (canMoveLater) {
            ImageControl(
                icon = UsIcons.Forward,
                description = "Move this photo later",
                onClick = { onMove(1) },
                tag = "seller_image_later:$key",
            )
        }
    }
}

@Composable
private fun ImageControl(
    icon: ImageVector,
    description: String,
    onClick: () -> Unit,
    tag: String,
    modifier: Modifier = Modifier,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(CONTROL_TARGET)
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(UsTheme.extended.glassBg)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            }
            .testTag(tag),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(CONTROL_GLYPH),
        )
    }
}

/** The dashed-feeling "add" square at the end of the row. */
@Composable
private fun AddCard(onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .width(CARD_WIDTH)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Add photos"
            }
            .testTag("seller_image_add"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(CARD_WIDTH)
                .clip(shape)
                .background(UsTheme.extended.bgCard)
                .border(HAIRLINE, UsTheme.extended.borderMedium, shape),
        ) {
            Icon(
                imageVector = UsIcons.ImagePlus,
                contentDescription = null,
                tint = UsTheme.extended.textSecondary,
                modifier = Modifier.size(ADD_GLYPH),
            )
        }
        Text(
            text = "Add photos",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
        )
    }
}

private val CARD_WIDTH = 110.dp
private val CONTROL_TARGET = 32.dp
private val CONTROL_GLYPH = 16.dp
private val ADD_GLYPH = 26.dp
private val HAIRLINE = 1.dp
private val COVER_BORDER = 2.dp
