package com.us.android.feature.commerce.seller

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.model.SellerProfile
import com.us.android.core.commerce.model.SellerStatus
import com.us.android.core.commerce.model.Variant
import com.us.android.core.commerce.model.VariantOption
import com.us.android.core.commerce.model.chooserLabel
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.CommerceSheet
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

            is SellerUiState.Content -> {
                s.chooser?.let { chooser ->
                    VariantChooserSheet(
                        chooser = chooser,
                        onChoose = { variant -> viewModel.chooseVariant(variant, actions.openStock) },
                        onDismiss = viewModel::dismissChooser,
                    )
                }
                SellerContent(
                    state = s,
                    padding = padding,
                    actions = actions,
                    // The row resolves the VARIANT before the editor opens;
                    // the product id never reaches the stock route.
                    onOpenProduct = { product -> viewModel.openStock(product, actions.openStock) },
                    onSubmitProduct = viewModel::submitProduct,
                    onDismissSubmitProblem = viewModel::dismissSubmitProblem,
                )
            }
        }
    }
}

@Suppress("LongParameterList")
@Composable
private fun SellerContent(
    state: SellerUiState.Content,
    padding: PaddingValues,
    actions: SellerHubActions,
    onOpenProduct: (SellerProduct) -> Unit,
    onSubmitProduct: (productId: String) -> Unit,
    onDismissSubmitProblem: () -> Unit,
) {
    val profile = state.profile
    val products = state.products
    LazyColumn(
        modifier = Modifier.padding(padding).testTag("mseller_hub"),
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

        state.message?.let { message ->
            item { CommerceNotice(text = message) }
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

        ordersSection(actions)

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
                    // A product row opens its stock editor FOR A VARIANT. The
                    // ViewModel reads the variants first (2026-09-30): the
                    // route takes a variant id, and the product id used to be
                    // sent in its place, which the server cannot look up.
                    onClick = { onOpenProduct(product) },
                    resolving = state.resolvingProductId == product.id,
                    // The other half of the step that was missing: a listing
                    // created in `draft` was never submitted, so it never
                    // appeared in search and the seller had no way to find out
                    // why. Offered only where the server takes it — a first
                    // submit for a draft, a resubmit after a rejection or a
                    // change request (2026-10-24; it used to be draft only, so
                    // a listing sent back by a reviewer was a dead end).
                    submit = product.submitOffer()?.let { offer ->
                        RowSubmit(
                            offer = offer,
                            submitting = state.submittingProductId == product.id,
                            // One submit at a time; the others wait rather
                            // than queue behind it unseen.
                            enabled = state.submittingProductId == null,
                            onSubmit = { onSubmitProduct(product.id) },
                        )
                    },
                    problem = state.submitProblem?.takeIf { it.productId == product.id },
                    onDismissProblem = onDismissSubmitProblem,
                    onEditImages = { actions.openImages(product.id, product.title) },
                )
            }
        }
    }
}

/**
 * The order surface, above the catalogue: on any day the shop is open, what
 * to pack matters before what is listed. Shown whatever the shop's status,
 * because a shop suspended today still has yesterday's orders to ship.
 */
private fun LazyListScope.ordersSection(actions: SellerHubActions) {
    item {
        Text(
            text = "Orders",
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
        )
    }
    item {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            HubTile(text = "Orders", onClick = actions.openOrders, modifier = Modifier.weight(1f))
            HubTile(text = "Returns", onClick = actions.openReturns, modifier = Modifier.weight(1f))
            HubTile(text = "Earnings", onClick = actions.openEarnings, modifier = Modifier.weight(1f))
        }
    }
}

/** A row's submit button: which one, and whether a submit is in flight. */
private data class RowSubmit(
    val offer: SubmitOffer,
    val submitting: Boolean,
    val enabled: Boolean,
    val onSubmit: () -> Unit,
)

@Suppress("LongParameterList")
@Composable
private fun SellerProductRow(
    product: SellerProduct,
    onClick: () -> Unit,
    submit: RowSubmit?,
    problem: SubmitProblem?,
    onDismissProblem: () -> Unit,
    onEditImages: () -> Unit,
    resolving: Boolean = false,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(UsTheme.radii.medium))
                .background(UsTheme.extended.bgCard)
                .pressScale(onClick = onClick)
                .padding(UsTheme.spacing.s)
                .testTag("mseller_product:${product.id}"),
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
                    text = when {
                        resolving -> "Opening"
                        reason == null -> "On sale"
                        else -> reason
                    },
                    style = MaterialTheme.typography.labelMedium,
                    color = if (reason == null) {
                        UsTheme.extended.textSecondary
                    } else {
                        UsTheme.extended.textPrimary
                    },
                )
                // The reviewer's own words, for a listing they sent back: the
                // thing the seller has to act on before Resubmit means anything.
                product.reviewRemark()?.let { remark ->
                    Text(
                        text = "Reviewer's note: $remark",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textSecondary,
                    )
                }
            }
        }

        // Always offered, and the wording says which case it is: a listing
        // with no picture is the one a seller most needs pushing towards.
        UsSecondaryButton(
            text = if (product.imageUrl.isNullOrBlank()) "Add photos" else "Edit photos",
            onClick = onEditImages,
            modifier = Modifier.fillMaxWidth(),
        )

        // Only where it applies. A product already under review, live or
        // hidden has nothing to submit, and a button that does nothing is
        // worse than no button. A rejected or sent-back listing is offered a
        // resubmit, which the server takes.
        submit?.let { s ->
            UsSecondaryButton(
                text = if (s.submitting) "Sending for review…" else s.offer.label,
                onClick = s.onSubmit,
                enabled = s.enabled && !s.submitting,
                modifier = Modifier.fillMaxWidth().testTag("mseller_submit:${product.id}"),
            )
        }

        problem?.let { SubmitProblemCard(problem = it, onDismiss = onDismissProblem) }
    }
}

/**
 * Why the server would not take a listing for review, under the row that
 * asked: the sentence, then every gap it named, so the seller can fix them
 * all before trying again rather than one refusal at a time.
 */
@Composable
private fun SubmitProblemCard(problem: SubmitProblem, onDismiss: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .padding(UsTheme.spacing.s)
            .testTag("mseller_submit_problem:${problem.productId}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = problem.message,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.statusDanger,
        )
        problem.gaps.forEach { gap ->
            Text(
                text = "• $gap",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textPrimary,
            )
        }
        UsSecondaryButton(text = "Dismiss", onClick = onDismiss, compact = true)
    }
}

/** One of the three order tiles: a card that reads as a place to go, not a form control. */
@Composable
private fun HubTile(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .pressScale(onClick = onClick)
            .padding(vertical = UsTheme.spacing.l, horizontal = UsTheme.spacing.s),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/**
 * Which variant to edit, for a product that has several (2026-09-30).
 *
 * Each row is the variant's options ("M · Blue"), or its SKU when it has
 * none, with the units available; tapping one opens the stock editor for
 * exactly that variant's id.
 */
@Composable
private fun VariantChooserSheet(
    chooser: VariantChooser,
    onChoose: (Variant) -> Unit,
    onDismiss: () -> Unit,
) {
    CommerceSheet(title = "Which variant?", onDismiss = onDismiss) {
        VariantChooserList(chooser = chooser, onChoose = onChoose)
    }
}

/** The chooser's body, apart from the sheet so it can be previewed. */
@Composable
private fun VariantChooserList(
    chooser: VariantChooser,
    onChoose: (Variant) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Text(
            text = chooser.product.title,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        chooser.variants.forEachIndexed { index, variant ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .background(UsTheme.extended.bgCard)
                    .pressScale(onClick = { onChoose(variant) })
                    .padding(UsTheme.spacing.m)
                    .testTag("mseller_variant:${variant.id}"),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = variant.chooserLabel(index),
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = "${variant.availableQty} available",
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.textSecondary,
                )
            }
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

@Preview(showBackground = true)
@Composable
private fun SellerContentPreview() {
    UsTheme {
        SellerContent(
            state = SellerUiState.Content(
                profile = SellerProfile(
                    id = "s-1",
                    storeName = "Asha's Kitchenware",
                    status = SellerStatus.APPROVED,
                    onboardingStep = 5,
                    state = "Karnataka",
                    city = "Bengaluru",
                    postalCode = "560001",
                    totalProducts = 2,
                    totalOrders = 7,
                ),
                products = listOf(
                    SellerProduct(
                        id = "p-1",
                        title = "Steel kettle, 1.7 L",
                        status = "active",
                        approvalStatus = "approved",
                        rejectionReason = null,
                        imageUrl = null,
                    ),
                    SellerProduct(
                        id = "p-2",
                        title = "Cast-iron tawa",
                        status = "draft",
                        approvalStatus = "draft",
                        rejectionReason = null,
                        imageUrl = null,
                    ),
                    SellerProduct(
                        id = "p-3",
                        title = "Brass diya, set of 4",
                        status = "draft",
                        approvalStatus = "changes_requested",
                        rejectionReason = "Add a photo that shows all four pieces.",
                        imageUrl = null,
                    ),
                ),
                submitProblem = SubmitProblem(
                    productId = "p-2",
                    message = "This listing isn't ready for review yet.",
                    gaps = listOf("Add at least one product photo.", "Choose a GST tax class."),
                ),
            ),
            padding = PaddingValues(),
            actions = SellerHubActions(
                openStock = { _, _ -> },
                openImages = { _, _ -> },
                openPickupAddress = {},
                listProduct = {},
                submitShop = {},
                openOrders = {},
                openReturns = {},
                openEarnings = {},
            ),
            onOpenProduct = {},
            onSubmitProduct = {},
            onDismissSubmitProblem = {},
        )
    }
}

@Preview(showBackground = true)
@Composable
@Suppress("MagicNumber")
private fun VariantChooserListPreview() {
    fun size(id: String, label: String, qty: Int) = Variant(
        id = id,
        sku = "TEE-$label",
        options = listOf(VariantOption("Size", label)),
        mrp = Paise(99_900),
        sellingPrice = Paise(79_900),
        inStock = qty > 0,
        availableQty = qty,
    )
    UsTheme {
        VariantChooserList(
            chooser = VariantChooser(
                product = SellerProduct(
                    id = "p-1",
                    title = "Cotton tee",
                    status = "active",
                    approvalStatus = "approved",
                    rejectionReason = null,
                    imageUrl = null,
                ),
                variants = listOf(size("v-1", "S", 4), size("v-2", "M", 0), size("v-3", "L", 12)),
            ),
            onChoose = {},
        )
    }
}
