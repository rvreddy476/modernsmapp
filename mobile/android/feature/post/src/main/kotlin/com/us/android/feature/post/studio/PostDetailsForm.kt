package com.us.android.feature.post.studio

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.createhub.HashtagMentionTransformation
import com.us.android.feature.post.createhub.ReelInputField
import com.us.android.feature.post.createhub.ReelLocationSheet
import com.us.android.feature.post.createhub.ReelOption
import com.us.android.feature.post.createhub.ReelOptionSheet
import com.us.android.feature.post.createhub.ReelSheet
import com.us.android.feature.post.createhub.SheetTitle
import com.us.android.feature.post.createhub.TaggedUser
import com.us.android.feature.post.data.dto.VISIBILITY_FRIENDS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE

/** One photo in the details screen's strip. [model] is whatever the image loader takes: a file or a URL. */
@Immutable
data class DetailsTile(
    val key: String,
    val model: Any?,
    val altText: String = "",
    val decorative: Boolean = false,
    /** The edit's colour look, so the strip shows the photo as it will post. */
    val colorMatrix: ColorMatrix? = null,
)

/** Everything the details form shows. */
@Immutable
data class PostDetailsFormState(
    val tiles: List<DetailsTile>,
    val description: String,
    val details: PostDetailsUi,
    val recentLocations: List<String> = emptyList(),
    val mentionQuery: String = "",
    val mentionResults: List<TaggedUser> = emptyList(),
    val posting: Boolean = false,
    val savingDraft: Boolean = false,
    val canPost: Boolean = true,
    /** The secondary button's word: "Drafts" in the studio, "Save" in the draft editor. */
    val draftsLabel: String = "Drafts",
    /** The post's language (BCP-47), edited under More options; null hides the row. */
    val language: String? = null,
)

/** What the details form can ask for. A null action hides its control. */
class PostDetailsFormActions(
    val onBack: () -> Unit,
    val onPreview: (() -> Unit)?,
    val onDescriptionChanged: (String) -> Unit,
    val onInsertHashtag: () -> Unit,
    val onMentionQueryChanged: (String) -> Unit,
    val onMentionPicked: (TaggedUser) -> Unit,
    val onDetails: ((PostDetailsUi) -> PostDetailsUi) -> Unit,
    val onLocationChosen: (String) -> Unit,
    val onAltText: ((index: Int, altText: String, decorative: Boolean) -> Unit)?,
    val onAddPhotos: (() -> Unit)?,
    val onDrafts: (() -> Unit)?,
    val onPost: () -> Unit,
    val onLanguageChanged: ((String) -> Unit)? = null,
)

private enum class DetailsSheet { AUDIENCE, LOCATION, LINK, MORE, DISCLOSURE, ALT }

/**
 * The post details screen (post-screens parity, 2026-10-18), shared by the
 * Studio's last step and the draft editor: the photos with the cover
 * marked, a title, the description with its # and @ helpers, the place,
 * a link, the audience, and More options; Drafts and Post at the foot.
 */
@Suppress("LongMethod", "CyclomaticComplexMethod")
@Composable
fun PostDetailsForm(
    state: PostDetailsFormState,
    actions: PostDetailsFormActions,
    modifier: Modifier = Modifier,
    /** What sits above the photos: a recovery or a failed publish to finish. */
    header: @Composable ColumnScope.() -> Unit = {},
) {
    var sheet by rememberSaveable { mutableStateOf<DetailsSheet?>(null) }
    var mentioning by rememberSaveable { mutableStateOf(false) }
    var expanded by rememberSaveable { mutableStateOf(false) }
    val details = state.details
    Column(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .statusBarsPadding()
            .imePadding()
            .testTag("post-details"),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.xs),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            IconButton(onClick = actions.onBack, modifier = Modifier.testTag("post-details-back")) {
                Icon(UsIcons.Back, contentDescription = "Back", tint = UsTheme.extended.textPrimary)
            }
            Spacer(Modifier.weight(1f))
            actions.onPreview?.let { preview ->
                TextButton(onClick = preview, modifier = Modifier.testTag("post-details-preview")) {
                    Text("Preview", color = UsTheme.extended.accentSolid, fontWeight = FontWeight.SemiBold)
                }
            }
        }
        Column(
            modifier = Modifier
                .weight(1f)
                .verticalScroll(rememberScrollState()),
        ) {
            header()
            MediaStrip(
                tiles = state.tiles,
                coverIndex = details.coverIndex,
                onCover = { index -> actions.onDetails { it.copy(coverIndex = index) } },
                onAdd = actions.onAddPhotos?.takeIf { state.tiles.size < MAX_PHOTOS },
            )
            if (state.tiles.size > 1) {
                Text(
                    text = "Tap a photo to make it the cover.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.l))
            PlainField(
                value = details.title,
                onValueChange = { value -> actions.onDetails { it.copy(title = value.take(PostDetailsUi.MAX_TITLE)) } },
                placeholder = "Add a title",
                singleLine = true,
                emphasis = true,
                tag = "post-details-title",
            )
            HorizontalDivider(
                color = UsTheme.extended.borderSubtle,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            )
            PlainField(
                value = state.description,
                onValueChange = actions.onDescriptionChanged,
                placeholder = "Say more about it — what's happening, who's in it",
                singleLine = false,
                minHeight = if (expanded) EXPANDED_DESCRIPTION else DESCRIPTION_HEIGHT,
                tag = "post-details-description",
            )
            Row(
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                HelperChip(icon = UsIcons.Hash, label = "Hashtags", onClick = actions.onInsertHashtag, tag = "post-details-hash")
                HelperChip(icon = UsIcons.AtSign, label = "Mention", onClick = { mentioning = !mentioning }, tag = "post-details-mention")
                HelperChip(
                    icon = if (expanded) UsIcons.Minimize else UsIcons.Maximize,
                    label = if (expanded) "Less room" else "More room",
                    onClick = { expanded = !expanded },
                    tag = "post-details-expand",
                )
            }
            if (mentioning) {
                MentionPicker(
                    query = state.mentionQuery,
                    results = state.mentionResults,
                    onQueryChanged = actions.onMentionQueryChanged,
                    onPick = { user ->
                        actions.onMentionPicked(user)
                        mentioning = false
                    },
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.l))
            HorizontalDivider(color = UsTheme.extended.borderSubtle)
            DetailRow(
                icon = UsIcons.MapPin,
                title = details.locationName.ifBlank { "Location" },
                value = null,
                onClick = { sheet = DetailsSheet.LOCATION },
                tag = "post-details-location",
            )
            val suggestions = state.recentLocations.filterNot { it.equals(details.locationName, ignoreCase = true) }
            if (suggestions.isNotEmpty()) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState())
                        .padding(horizontal = UsTheme.spacing.pageHorizontal)
                        .testTag("post-details-recent-locations"),
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                ) {
                    suggestions.forEach { place ->
                        HelperChip(icon = null, label = place, onClick = { actions.onLocationChosen(place) }, tag = "post-details-place")
                    }
                }
                Spacer(Modifier.height(UsTheme.spacing.m))
            }
            DetailRow(
                icon = UsIcons.Link,
                title = "Add link",
                value = PostLink.normalize(details.link),
                problem = details.linkProblem,
                onClick = { sheet = DetailsSheet.LINK },
                tag = "post-details-link",
            )
            DetailRow(
                icon = audienceIcon(details.visibility),
                title = audienceLabel(details.visibility),
                value = null,
                onClick = { sheet = DetailsSheet.AUDIENCE },
                tag = "post-details-audience",
            )
            DetailRow(
                icon = UsIcons.Sliders,
                title = "More options",
                value = null,
                onClick = { sheet = DetailsSheet.MORE },
                tag = "post-details-more",
            )
            Spacer(Modifier.height(UsTheme.spacing.xxl))
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            actions.onDrafts?.let { drafts ->
                UsSecondaryButton(
                    text = if (state.savingDraft) "Saving…" else state.draftsLabel,
                    onClick = drafts,
                    enabled = !state.savingDraft && !state.posting && details.valid,
                    modifier = Modifier.weight(1f).testTag("post-details-drafts"),
                )
            }
            UsButton(
                text = "Post",
                onClick = actions.onPost,
                enabled = state.canPost && !state.savingDraft,
                loading = state.posting,
                modifier = Modifier.weight(1f).testTag("post-details-post"),
            )
        }
    }

    when (sheet) {
        DetailsSheet.AUDIENCE -> ReelOptionSheet(
            title = "Who can view",
            options = AUDIENCE_OPTIONS,
            selected = details.visibility,
            onPick = { value ->
                actions.onDetails { it.copy(visibility = value) }
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        DetailsSheet.LOCATION -> ReelLocationSheet(
            initial = details.locationName,
            onDone = { place ->
                actions.onLocationChosen(place)
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        DetailsSheet.LINK -> LinkSheet(
            initial = details.link,
            onDone = { link ->
                actions.onDetails { it.copy(link = link.trim()) }
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        DetailsSheet.MORE -> MoreOptionsSheet(
            details = details,
            language = state.language,
            onLanguageChanged = actions.onLanguageChanged,
            hasPhotos = state.tiles.isNotEmpty() && actions.onAltText != null,
            onDetails = actions.onDetails,
            onOpenDisclosure = { sheet = DetailsSheet.DISCLOSURE },
            onOpenAlt = { sheet = DetailsSheet.ALT },
            onDismiss = { sheet = null },
        )
        DetailsSheet.DISCLOSURE -> DisclosureSheet(details = details, onDetails = actions.onDetails, onDismiss = { sheet = DetailsSheet.MORE })
        DetailsSheet.ALT -> actions.onAltText?.let { onAlt ->
            AltTextSheet(tiles = state.tiles, onAltText = onAlt, onDismiss = { sheet = DetailsSheet.MORE })
        }
        null -> Unit
    }
}

@Composable
private fun MediaStrip(tiles: List<DetailsTile>, coverIndex: Int, onCover: (Int) -> Unit, onAdd: (() -> Unit)?) {
    LazyRow(
        modifier = Modifier.fillMaxWidth().testTag("post-details-strip"),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = UsTheme.spacing.pageHorizontal),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        itemsIndexed(tiles, key = { _, tile -> tile.key }) { index, tile ->
            val cover = index == coverIndex || tiles.size == 1
            Box(
                modifier = Modifier
                    .width(TILE_WIDTH)
                    .height(TILE_HEIGHT)
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .background(UsTheme.extended.bgRaised)
                    .then(
                        if (cover) {
                            Modifier.border(COVER_RING, UsTheme.extended.accent, RoundedCornerShape(UsTheme.radii.medium))
                        } else {
                            Modifier
                        },
                    )
                    .clickable(role = Role.Button, onClickLabel = "Make this the cover") { onCover(index) }
                    .semantics { contentDescription = if (cover) "Photo ${index + 1}, the cover" else "Photo ${index + 1}" }
                    .testTag("post-details-tile:$index"),
            ) {
                AsyncImage(
                    model = tile.model,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    colorFilter = tile.colorMatrix?.let { ColorFilter.colorMatrix(it) },
                    modifier = Modifier.fillMaxSize(),
                )
                Text(
                    text = "${index + 1}",
                    style = MaterialTheme.typography.labelSmall,
                    color = UsTheme.extended.onMedia,
                    modifier = Modifier
                        .align(Alignment.TopEnd)
                        .padding(UsTheme.spacing.xs)
                        .clip(CircleShape)
                        .background(UsTheme.extended.mediaPlate)
                        .padding(horizontal = UsTheme.spacing.s),
                )
                if (cover) {
                    Text(
                        text = "Cover",
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.SemiBold,
                        color = UsTheme.extended.onMedia,
                        modifier = Modifier
                            .align(Alignment.BottomCenter)
                            .fillMaxWidth()
                            .background(UsTheme.extended.mediaPlate)
                            .padding(vertical = UsTheme.spacing.xs)
                            .testTag("post-details-cover"),
                        textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                    )
                }
            }
        }
        if (onAdd != null) {
            item(key = "add") {
                Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier
                        .width(TILE_WIDTH)
                        .height(TILE_HEIGHT)
                        .clip(RoundedCornerShape(UsTheme.radii.medium))
                        .background(UsTheme.extended.bgCard)
                        .border(HAIRLINE, UsTheme.extended.borderMedium, RoundedCornerShape(UsTheme.radii.medium))
                        .clickable(role = Role.Button, onClickLabel = "Add photos", onClick = onAdd)
                        .testTag("post-details-add"),
                ) {
                    Icon(UsIcons.Create, contentDescription = "Add photos", tint = UsTheme.extended.textMuted)
                }
            }
        }
    }
}

@Composable
private fun PlainField(
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    singleLine: Boolean,
    tag: String,
    emphasis: Boolean = false,
    minHeight: androidx.compose.ui.unit.Dp = androidx.compose.ui.unit.Dp.Unspecified,
) {
    val style = (if (emphasis) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyLarge)
        .copy(color = UsTheme.extended.textPrimary)
    val accent = UsTheme.extended.accentSolid
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .then(if (minHeight != androidx.compose.ui.unit.Dp.Unspecified) Modifier.heightIn(min = minHeight) else Modifier),
    ) {
        if (value.isEmpty()) Text(placeholder, style = style, color = UsTheme.extended.textDim)
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            singleLine = singleLine,
            textStyle = style,
            cursorBrush = SolidColor(accent),
            visualTransformation = if (singleLine) {
                androidx.compose.ui.text.input.VisualTransformation.None
            } else {
                remember(accent) { HashtagMentionTransformation(accent) }
            },
            modifier = Modifier
                .fillMaxWidth()
                .semantics { contentDescription = placeholder }
                .testTag(tag),
        )
    }
}

@Composable
private fun HelperChip(icon: ImageVector?, label: String, onClick: () -> Unit, tag: String) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.fillSubtle)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        if (icon != null) Icon(icon, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(CHIP_ICON))
        Text(label, style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textPrimary, maxLines = 1)
    }
}

@Composable
private fun MentionPicker(
    query: String,
    results: List<TaggedUser>,
    onQueryChanged: (String) -> Unit,
    onPick: (TaggedUser) -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("post-details-mention-picker"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        ReelInputField(value = query, onValueChange = onQueryChanged, placeholder = "Search people", icon = UsIcons.Search)
        results.take(MAX_MENTION_RESULTS).forEach { user ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .clickable(role = Role.Button) { onPick(user) }
                    .padding(vertical = UsTheme.spacing.s)
                    .testTag("post-details-mention:${user.username}"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                UsAvatar(name = user.name.ifBlank { user.username }, seed = user.id, size = UsAvatarSize.Small)
                Column(modifier = Modifier.weight(1f)) {
                    Text(user.name.ifBlank { user.username }, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
                    Text("@${user.username}", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                }
            }
        }
    }
}

@Composable
private fun DetailRow(
    icon: ImageVector,
    title: String,
    value: String?,
    onClick: () -> Unit,
    tag: String,
    problem: String? = null,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(icon, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(ROW_ICON))
        Column(modifier = Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary, maxLines = 1, overflow = TextOverflow.Ellipsis)
            value?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted, maxLines = 1, overflow = TextOverflow.Ellipsis) }
            problem?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.statusDanger) }
        }
        Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(ROW_ICON))
    }
}

@Composable
private fun LinkSheet(initial: String, onDone: (String) -> Unit, onDismiss: () -> Unit) {
    var draft by rememberSaveable { mutableStateOf(initial) }
    ReelSheet(onDismiss = onDismiss, testTag = "post-details-link-sheet") {
        SheetTitle("Add link")
        Spacer(Modifier.height(UsTheme.spacing.l))
        ReelInputField(value = draft, onValueChange = { draft = it }, placeholder = "https://", icon = UsIcons.Link, onDone = {
            if (PostLink.problem(draft) == null) onDone(draft)
        })
        PostLink.problem(draft)?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.statusDanger, modifier = Modifier.padding(top = UsTheme.spacing.s))
        }
        Spacer(Modifier.height(UsTheme.spacing.xxl))
        UsButton(text = "Done", onClick = { onDone(draft) }, enabled = PostLink.problem(draft) == null, modifier = Modifier.fillMaxWidth())
        if (initial.isNotBlank()) {
            TextButton(onClick = { onDone("") }, modifier = Modifier.fillMaxWidth()) {
                Text("Remove link", color = UsTheme.extended.textMuted)
            }
        }
    }
}

@Composable
private fun MoreOptionsSheet(
    details: PostDetailsUi,
    language: String?,
    onLanguageChanged: ((String) -> Unit)?,
    hasPhotos: Boolean,
    onDetails: ((PostDetailsUi) -> PostDetailsUi) -> Unit,
    onOpenDisclosure: () -> Unit,
    onOpenAlt: () -> Unit,
    onDismiss: () -> Unit,
) {
    ReelSheet(onDismiss = onDismiss, testTag = "post-details-more-sheet") {
        SheetTitle("More options")
        SectionLabel("Privacy")
        SwitchRow(
            title = "Allow comments",
            detail = null,
            checked = details.allowComments,
            onChange = { on -> onDetails { it.copy(allowComments = on) } },
            tag = "post-details-allow-comments",
        )
        SwitchRow(
            title = "Allow reuse of content",
            detail = "Others can remix your photos and use them in their own posts.",
            checked = details.allowReuse,
            onChange = { on -> onDetails { it.copy(allowReuse = on) } },
            tag = "post-details-allow-reuse",
        )
        SectionLabel("Advanced")
        SheetLinkRow(
            title = "Content disclosure",
            detail = when {
                details.paidPromotion && details.isBranded -> "Paid partnership, your brand"
                details.paidPromotion -> "Paid partnership"
                details.isBranded -> "Your brand"
                else -> "Say when a post promotes a brand"
            },
            onClick = onOpenDisclosure,
            tag = "post-details-disclosure",
        )
        if (hasPhotos) {
            SheetLinkRow(title = "Add alternative text", detail = "Describe each photo for people who can't see it", onClick = onOpenAlt, tag = "post-details-alt")
        }
        SwitchRow(
            title = "AI-generated content",
            detail = "Turn this on when AI made or substantially changed what people will see. It adds a label to the post.",
            checked = details.aiGenerated,
            onChange = { on -> onDetails { it.copy(aiGenerated = on) } },
            tag = "post-details-ai",
        )
        if (language != null && onLanguageChanged != null) {
            SectionLabel("Language")
            ReelInputField(
                value = language,
                onValueChange = onLanguageChanged,
                placeholder = "Post language, like en",
                icon = UsIcons.Globe,
                modifier = Modifier.testTag("post-details-language"),
            )
        }
    }
}

@Composable
private fun DisclosureSheet(details: PostDetailsUi, onDetails: ((PostDetailsUi) -> PostDetailsUi) -> Unit, onDismiss: () -> Unit) {
    ReelSheet(onDismiss = onDismiss, testTag = "post-details-disclosure-sheet") {
        SheetTitle("Content disclosure")
        Text(
            "Say when this post promotes a product, a service or a brand. A label shows on the post.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(UsTheme.spacing.m),
        )
        SwitchRow(
            title = "Paid partnership",
            detail = "You were paid or given something to make it.",
            checked = details.paidPromotion,
            onChange = { on -> onDetails { it.copy(paidPromotion = on) } },
            tag = "post-details-paid",
        )
        SwitchRow(
            title = "Your brand",
            detail = "It promotes your own business or products.",
            checked = details.isBranded,
            onChange = { on -> onDetails { it.copy(isBranded = on) } },
            tag = "post-details-branded",
        )
        UsButton(text = "Done", onClick = onDismiss, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l))
    }
}

@Composable
private fun AltTextSheet(
    tiles: List<DetailsTile>,
    onAltText: (index: Int, altText: String, decorative: Boolean) -> Unit,
    onDismiss: () -> Unit,
) {
    ReelSheet(onDismiss = onDismiss, testTag = "post-details-alt-sheet") {
        SheetTitle("Alternative text")
        Column(modifier = Modifier.heightIn(max = ALT_SHEET_MAX).verticalScroll(rememberScrollState())) {
            tiles.forEachIndexed { index, tile ->
                Row(
                    modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.m),
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    AsyncImage(
                        model = tile.model,
                        contentDescription = null,
                        contentScale = ContentScale.Crop,
                        modifier = Modifier.size(ALT_THUMB).clip(RoundedCornerShape(UsTheme.radii.small)),
                    )
                    Box(modifier = Modifier.weight(1f)) {
                        ReelInputField(
                            value = tile.altText,
                            onValueChange = { onAltText(index, it, false) },
                            placeholder = "Describe photo ${index + 1}",
                            icon = UsIcons.Eye,
                            modifier = Modifier.testTag("post-details-alt:$index"),
                        )
                    }
                }
            }
        }
        UsButton(text = "Done", onClick = onDismiss, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l))
    }
}

@Composable
private fun ColumnScope.SectionLabel(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.padding(start = UsTheme.spacing.m, top = UsTheme.spacing.xxl, bottom = UsTheme.spacing.s),
    )
}

@Composable
private fun SwitchRow(title: String, detail: String?, checked: Boolean, onChange: (Boolean) -> Unit, tag: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Switch) { onChange(!checked) }
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            detail?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
        }
        Spacer(Modifier.width(UsTheme.spacing.l))
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
private fun SheetLinkRow(title: String, detail: String, onClick: () -> Unit, tag: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            Text(detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
        Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(ROW_ICON))
    }
}

private fun audienceIcon(visibility: String): ImageVector = when (visibility) {
    VISIBILITY_FRIENDS -> UsIcons.Friends
    VISIBILITY_PRIVATE -> UsIcons.Lock
    else -> UsIcons.Globe
}

/** The audience sheet: Everyone, Friends (mutual follows), Only you. */
internal val AUDIENCE_OPTIONS = listOf(
    ReelOption(AUDIENCES[0], "Everyone", "Anyone can see it"),
    ReelOption(AUDIENCES[1], "Friends", "Followers you follow back"),
    ReelOption(AUDIENCES[2], "Only you", "Nobody else can see it"),
)

private const val MAX_PHOTOS = 10
private const val MAX_MENTION_RESULTS = 6
private val TILE_WIDTH = 72.dp
private val TILE_HEIGHT = 96.dp
private val COVER_RING = 2.dp
private val HAIRLINE = 1.dp
private val CHIP_ICON = 16.dp
private val ROW_ICON = 20.dp
private val DESCRIPTION_HEIGHT = 96.dp
private val EXPANDED_DESCRIPTION = 240.dp
private val ALT_THUMB = 56.dp
private val ALT_SHEET_MAX = 420.dp
