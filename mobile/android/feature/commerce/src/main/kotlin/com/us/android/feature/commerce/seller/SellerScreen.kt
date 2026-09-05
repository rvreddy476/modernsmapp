package com.us.android.feature.commerce.seller

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.model.SellerProfile
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar
import com.us.android.feature.commerce.ui.pressScale

/**
 * The seller hub.
 *
 * One screen answering the two questions a seller opens the app with: can I
 * sell, and what is the state of my listings. Both are shown together because
 * the second is misleading without the first — a catalogue of products that
 * look fine, under an application still awaiting review, is how a seller
 * concludes their listings are broken when their shop simply is not open yet.
 */
@Composable
fun SellerScreen(
    onBack: () -> Unit,
    actions: SellerHubActions,
    onStartSelling: () -> Unit,
    viewModel: SellerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { MSellerPageBar(title = "My shop", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            is SellerUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading your shop",
            )

            is SellerUiState.NotASeller -> Column(
                modifier = Modifier
                    .padding(padding)
                    .padding(UsTheme.spacing.pageHorizontal),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                UsEmptyState(
                    title = "You do not have a shop yet",
                    detail = "Open one to list products and take orders.",
                )
                UsSecondaryButton(
                    text = "Start selling",
                    onClick = onStartSelling,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            is SellerUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is SellerUiState.Content -> SellerContent(
                profile = s.profile,
                products = s.products,
                padding = padding,
                actions = actions,
            )
        }
    }
}

@Composable
private fun SellerContent(
    profile: SellerProfile,
    products: List<SellerProduct>,
    padding: PaddingValues,
    actions: SellerHubActions,
) {
    LazyColumn(
        modifier = Modifier.padding(padding),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.m,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        item { ShopHeader(profile) }

        // The banner is the point of the header. A seller whose shop is not
        // approved needs to be told so before they spend an hour wondering why
        // nothing sells.
        profile.status.guidance()?.let { guidance ->
            item { CommerceNotice(text = guidance) }
        }

        // The step that was missing entirely: a shop in draft had no way to
        // be sent for review, so no seller could ever be approved and nothing
        // they listed could go on sale.
        if (profile.status.canSubmit) {
            item {
                UsButton(
                    text = "Submit for review",
                    onClick = actions.submitShop,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }

        item {
            UsButton(
                text = "List a product",
                onClick = actions.listProduct,
                modifier = Modifier.fillMaxWidth(),
            )
        }

        item {
            UsSecondaryButton(
                text = "Pickup address",
                onClick = actions.openPickupAddress,
                modifier = Modifier.fillMaxWidth(),
            )
        }

        item {
            Text(
                text = "Products",
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
        }

        if (products.isEmpty()) {
            item {
                Text(
                    text = "Nothing listed yet.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
            }
        } else {
            items(products, key = { it.id }) { product ->
                SellerProductRow(
                    product = product,
                    // A product row opens its stock editor. The variant id is
                    // the product id here only because the P0 catalogue is
                    // single-variant; the route takes a variant id so this
                    // stays correct when it is not.
                    onClick = { actions.openStock(product.id, product.title) },
                    // The other half of the step that was missing: a listing
                    // created in `draft` was never submitted, so it never
                    // appeared in search and the seller had no way to find out
                    // why. Offered only where it applies — a product already
                    // under review or rejected has nothing to submit.
                    onSubmit = { actions.submitProduct(product.id) }
                        .takeIf { product.approvalStatus == "draft" },
                    onEditImages = { actions.openImages(product.id, product.title) },
                )
            }
        }
    }
}

@Composable
private fun SellerProductRow(
    product: SellerProduct,
    onClick: () -> Unit,
    onSubmit: (() -> Unit)?,
    onEditImages: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(UsTheme.radii.medium))
                .background(UsTheme.extended.bgCard)
                .pressScale(onClick = onClick)
                .padding(UsTheme.spacing.s),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CommerceImage(
                url = product.imageUrl,
                contentDescription = product.title,
                modifier = Modifier.size(56.dp),
            )
            Column(
                modifier = Modifier.fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Text(
                    text = product.title,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                // Why it is not on sale, in the seller's terms. Showing the raw
                // `status` / `approval_status` pair would make the seller reverse
                // engineer a state machine to learn that moderation rejected them.
                val reason = product.notLiveReason()
                Text(
                    text = reason ?: "On sale",
                    style = MaterialTheme.typography.labelMedium,
                    color = if (reason == null) {
                        UsTheme.extended.textSecondary
                    } else {
                        UsTheme.extended.textPrimary
                    },
                )
            }
        }

        // Always offered, and the wording says which case it is: a listing
        // with no picture is the one a seller most needs pushing towards.
        UsSecondaryButton(
            text = if (product.imageUrl.isNullOrBlank()) "Add photos" else "Edit photos",
            onClick = onEditImages,
            modifier = Modifier.fillMaxWidth(),
        )

        // Only where it applies. A product already under review, approved or
        // rejected has nothing to submit, and a button that does nothing is
        // worse than no button.
        onSubmit?.let { submit ->
            UsSecondaryButton(
                text = "Submit for review",
                onClick = submit,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

/** The shop's name and where it stands with review. */
@Composable
private fun ShopHeader(profile: SellerProfile) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Text(
            text = profile.storeName.ifBlank { "Your shop" },
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = profile.status.label(),
            style = MaterialTheme.typography.bodyMedium,
            color = if (profile.status.canSell) {
                UsTheme.extended.textPrimary
            } else {
                UsTheme.extended.textSecondary
            },
        )
    }
}
