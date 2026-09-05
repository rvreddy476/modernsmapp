package com.us.android.feature.commerce.seller

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar

/**
 * A product's photos, on their own screen.
 *
 * Reached from the seller hub for a listing that already exists. The gallery
 * that arrives is the one the server has; saving replaces it, in the order
 * shown, cover first.
 */
@Composable
fun ProductImagesScreen(
    productId: String,
    title: String,
    onBack: () -> Unit,
    onSaved: () -> Unit,
    viewModel: ProductImagesViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(productId) { viewModel.load(productId) }

    UsScaffold(
        topBar = {
            MSellerPageBar(title = title.ifBlank { "Photos" }, onBack = onBack)
        },
    ) { padding ->
        if (state.loading) {
            UsLoadingState(modifier = Modifier.padding(padding), label = "Loading photos")
            return@UsScaffold
        }
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(vertical = UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            ProductImagesSection(
                state = state,
                onPicked = viewModel::onPicked,
                onRemove = viewModel::remove,
                onMove = viewModel::move,
                onMakeCover = viewModel::makeCover,
            )

            CommerceNotice(
                text = "Photos are checked before they appear to buyers, like the listing itself.",
            )

            UsButton(
                text = "Save photos",
                onClick = { viewModel.attach(productId, onSaved) },
                // Disabled while anything is still uploading: sending now
                // would attach a gallery missing exactly the photos the seller
                // is watching upload.
                enabled = state.canAttach,
                loading = state.attaching,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}
