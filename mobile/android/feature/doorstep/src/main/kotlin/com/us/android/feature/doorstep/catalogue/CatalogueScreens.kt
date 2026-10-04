package com.us.android.feature.doorstep.catalogue

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.data.CategorySummaryDto
import com.us.android.feature.doorstep.domain.GenderRules
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.model.toShortRupeeText
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.InfoNote
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.Pill
import com.us.android.feature.doorstep.ui.PriceText
import com.us.android.feature.doorstep.ui.Tone
import com.us.android.feature.doorstep.ui.durationText
import com.us.android.feature.doorstep.ui.listPadding

/** Doorstep's home: the categories of the customer's city, the dues banner, and the way to their bookings. */
@Composable
fun CatalogueScreen(
    onBack: () -> Unit,
    onOpenCategory: (slug: String) -> Unit,
    onOpenBookings: () -> Unit,
    onOpenOutstanding: () -> Unit,
    viewModel: CatalogueViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.load()
        onPauseOrDispose { }
    }

    DoorstepScreen(
        title = "Doorstep",
        onBack = onBack,
        actions = {
            IconButton(onClick = onOpenBookings) {
                Icon(UsIcons.FileText, contentDescription = "My bookings", tint = UsTheme.extended.textPrimary)
            }
        },
    ) { padding ->
        when {
            state.loading && state.categories.isEmpty() -> LoadingPane()
            state.unavailable -> MessagePane(
                title = "Doorstep isn't here yet",
                body = "Home services are opening city by city. We'll let you know when Doorstep reaches you.",
                icon = UsIcons.Wrench,
                secondaryLabel = "My bookings",
                onSecondary = onOpenBookings,
            )
            state.error != null && state.categories.isEmpty() -> MessagePane(
                title = "Couldn't load services",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            else -> LazyVerticalGrid(
                columns = GridCells.Fixed(2),
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                item(span = { GridItemSpan(maxLineSpan) }) {
                    Column {
                        Text(
                            text = "Services at your doorstep",
                            style = MaterialTheme.typography.headlineSmall,
                            color = UsTheme.extended.textPrimary,
                        )
                        Text(
                            text = "Fixed prices, GST included · ${state.cityName}",
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                }
                if (state.blocked) {
                    item(span = { GridItemSpan(maxLineSpan) }) {
                        DuesBanner(total = Paise(state.outstanding?.totalPaise ?: 0), onPay = onOpenOutstanding)
                    }
                }
                items(state.categories, key = { it.id }) { category ->
                    CategoryTile(category = category, onClick = { onOpenCategory(category.slug) })
                }
            }
        }
    }
}

@Composable
internal fun DuesBanner(total: Paise, onPay: () -> Unit) {
    DoorstepCard(highlighted = true) {
        Text("Pending dues", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.statusWarning)
        Text(
            text = "You have ${total.toRupeeText()} of unpaid extras from an earlier visit. Pay it to book again.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
        UsPillButton(text = "Pay dues", onClick = onPay)
    }
}

@Composable
private fun CategoryTile(category: CategorySummaryDto, onClick: () -> Unit) {
    DoorstepCard(onClick = onClick) {
        Box(
            modifier = Modifier
                .size(44.dp)
                .background(UsTheme.extended.accentSolid.copy(alpha = ICON_PLATE_ALPHA), RoundedCornerShape(UsTheme.radii.medium)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(familyIcon(category.family), contentDescription = null, tint = UsTheme.extended.accentSolid)
        }
        Text(
            text = category.name,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = "From ${Paise(category.startingPricePaise).toShortRupeeText()}",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** Lucide glyphs by family: the identity is the label; the glyph only helps the eye. */
internal fun familyIcon(family: String): ImageVector = when (family) {
    "BEAUTY_SALON" -> UsIcons.Scissors
    "PAINTING" -> UsIcons.SquarePen
    "PEST_CONTROL" -> UsIcons.CircleSlash
    "HOME_CLEANING" -> UsIcons.Home
    else -> UsIcons.Wrench
}

@Composable
fun CategoryScreen(
    onBack: () -> Unit,
    onOpenService: (serviceId: String) -> Unit,
    viewModel: CategoryViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    DoorstepScreen(title = state.category?.name ?: "Services", onBack = onBack) { padding ->
        when {
            state.loading -> LoadingPane()
            state.error != null -> MessagePane(
                title = "Couldn't load this category",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                state.category?.let { category ->
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                            Text(category.description, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                            GenderRules.fixedRuleNote(category.genderRule)?.let { InfoNote(it, tone = Tone.Accent) }
                        }
                    }
                }
                items(state.services, key = { it.id }) { service ->
                    DoorstepCard(onClick = { onOpenService(service.id) }) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(
                                    service.name,
                                    style = MaterialTheme.typography.titleMedium,
                                    color = UsTheme.extended.textPrimary,
                                )
                                if (service.description.isNotBlank()) {
                                    Text(
                                        service.description,
                                        style = MaterialTheme.typography.bodySmall,
                                        color = UsTheme.extended.textMuted,
                                    )
                                }
                            }
                            Spacer(Modifier.width(UsTheme.spacing.m))
                            Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textDim)
                        }
                        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                            PriceText(
                                price = Paise(service.startingPricePaise),
                                mrp = service.startingMrpPaise?.let(::Paise),
                                modifier = Modifier.weight(1f),
                            )
                            Pill(durationText(service.durationMinutes), Tone.Neutral)
                        }
                    }
                }
                if (state.services.isEmpty()) {
                    item {
                        Text(
                            "No services are priced here yet.",
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textMuted,
                            modifier = Modifier.padding(top = UsTheme.spacing.xxl),
                        )
                    }
                }
            }
        }
    }
}

private const val ICON_PLATE_ALPHA = 0.14f
