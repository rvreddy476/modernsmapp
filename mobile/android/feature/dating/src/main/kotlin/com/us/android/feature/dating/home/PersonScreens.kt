package com.us.android.feature.dating.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.Tone

/**
 * The swipeable gallery: the person's approved photos, primary first.
 *
 * Each page loads the url the view model already resolved from that photo's
 * OWN state, so a blurred entry stays blurred beside a full one. A person with
 * no gallery falls back to the single card photo; with neither, the
 * placeholder. Nothing here re-decides a variant.
 */
@Composable
fun PersonGallery(
    photos: List<GalleryPhotoUi>,
    fallbackUrl: String?,
    name: String?,
    modifier: Modifier = Modifier,
) {
    val urls = photos.map { it.url }.ifEmpty { listOfNotNull(fallbackUrl) }
    if (urls.isEmpty()) {
        DatingPhoto(url = null, contentDescription = name, modifier = modifier)
        return
    }
    if (urls.size == 1) {
        DatingPhoto(url = urls.single(), contentDescription = photoDescription(name), modifier = modifier)
        return
    }
    val pager = rememberPagerState(pageCount = { urls.size })
    Box(modifier) {
        HorizontalPager(state = pager, modifier = Modifier.fillMaxSize()) { page ->
            DatingPhoto(
                url = urls[page],
                contentDescription = photoDescription(name, page + 1, urls.size),
                modifier = Modifier.fillMaxSize(),
            )
        }
        Row(
            modifier = Modifier.align(Alignment.TopCenter).padding(top = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            urls.indices.forEach { index ->
                val active = index == pager.currentPage
                Box(
                    Modifier
                        .size(if (active) DOT_ACTIVE else DOT)
                        .clip(CircleShape)
                        .background(
                            if (active) UsTheme.extended.accentSolid else UsTheme.extended.textDim.copy(alpha = 0.5f),
                        ),
                )
            }
        }
    }
}

private fun photoDescription(name: String?, index: Int? = null, total: Int? = null): String {
    val who = name?.takeIf { it.isNotBlank() }?.let { "$it's photo" } ?: "Their photo"
    return if (index != null && total != null) "$who, $index of $total" else who
}

/**
 * The readable half of a card: the description someone wrote, their prompt
 * answers and their languages.
 *
 * An empty member renders NOTHING — no heading over an absent bio, no empty
 * prompt list — so a sparse profile stays a photo and a name.
 */
@Composable
fun PersonDetailBody(detail: PersonDetailUi?, modifier: Modifier = Modifier) {
    if (detail == null) return
    val hasText = detail.bio != null || detail.prompts.isNotEmpty() || detail.languages.isNotEmpty()
    if (!hasText) return
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        detail.bio?.let { bio ->
            Text(
                text = bio,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
        }
        if (detail.prompts.isNotEmpty()) {
            detail.prompts.forEach { prompt ->
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = UsTheme.spacing.m)
                        .clip(RoundedCornerShape(UsTheme.radii.large))
                        .background(UsTheme.extended.bgRaised)
                        .padding(UsTheme.spacing.l),
                    verticalArrangement = Arrangement.spacedBy(2.dp),
                ) {
                    Text(
                        text = prompt.question,
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = UsTheme.extended.textDim,
                    )
                    Text(
                        text = prompt.answer,
                        style = MaterialTheme.typography.bodyLarge,
                        color = UsTheme.extended.textPrimary,
                    )
                }
            }
        }
        if (detail.languages.isNotEmpty()) {
            SectionLabel("Languages")
            Text(
                text = detail.languages.joinToString(", "),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}

/**
 * One person on their own screen (`GET /people/:userId`), with the same
 * pre-match detail the deck and an incoming spark show, so a decision can be
 * made here too. The decision itself stays where it belongs — this is a read.
 */
@Composable
fun PersonScreen(
    onBack: () -> Unit,
    viewModel: PersonViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    DatingScreen(title = "Profile", onBack = onBack) { padding ->
        when (val s = state) {
            PersonState.Loading -> LoadingPane()
            is PersonState.Gone -> MessagePane(
                title = "Not available",
                body = s.message,
                icon = UsIcons.Profile,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            is PersonState.Failed -> MessagePane(
                title = "Profile didn't load",
                body = s.message,
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
            )
            is PersonState.Loaded -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(padding)
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                PersonGallery(
                    photos = s.person.detail?.gallery.orEmpty(),
                    fallbackUrl = s.person.photoUrl,
                    name = s.person.name,
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(PHOTO_RATIO)
                        .clip(RoundedCornerShape(UsTheme.radii.card)),
                )
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    Text(
                        text = personLine(s.person.name, s.person.age) ?: "Someone",
                        style = MaterialTheme.typography.headlineSmall,
                        color = UsTheme.extended.textPrimary,
                    )
                    if (s.person.verified) Pill("Verified", Tone.Positive)
                }
                // The same line the deck card carries, so the person view is no
                // thinner than the card it came from. Every part is optional and
                // an absent one contributes no separator.
                val about = listOfNotNull(s.person.city, s.person.intent, s.person.distance).joinToString(" · ")
                if (about.isNotBlank()) {
                    Text(about, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                }
                // Absent when they hide last active. Nothing is shown then — no
                // placeholder, no empty row.
                s.person.lastActive?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                }
                PersonDetailBody(s.person.detail)
            }
        }
    }
}

private val DOT = 6.dp
private val DOT_ACTIVE = 8.dp
internal const val PHOTO_RATIO = 0.82f
