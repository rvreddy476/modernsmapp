package com.us.android.feature.commerce.tryon

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.facear.ui.FaceArTryOnSurface
import com.us.android.core.facear.ui.TryOnSurfaceActions
import com.us.android.core.facear.ui.TryOnSurfaceState
import com.us.android.core.facear.ui.captureCaption
import com.us.android.core.facear.ui.shareCapture
import com.us.android.core.facear.ui.stampCapture
import com.us.android.core.facear.ui.writeCapture
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MStorePageBar
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * The full-screen try-on.
 *
 * This screen owns the product side — the bar, the shade the buyer arrived
 * with, what a capture becomes — and nothing about Face AR. The camera, the
 * effect lifecycle, the shade carousel and the compare control are
 * [FaceArTryOnSurface] in `:core:facear`, which is what keeps the Banuba SDK
 * out of this feature module and lets a second product (a profile photo, a
 * reel) reuse the same surface without a `:feature:` → `:feature:` edge.
 *
 * The capture path is here rather than in the surface for the same reason it
 * always was: the surface produces a bitmap, and only this module knows the
 * product's name and the shade's, which is what makes a shared picture mean
 * anything. See `captureCaption`.
 */
@Composable
fun TryOnScreen(
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: TryOnViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = {
            MStorePageBar(
                title = (state as? TryOnUiState.Content)?.productTitle ?: "Try on",
                onBack = onBack,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            TryOnUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading try-on",
            )

            is TryOnUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::retry.takeIf { s.retryable },
            )

            is TryOnUiState.Content -> TryOnContent(
                state = s,
                viewModel = viewModel,
                modifier = Modifier.padding(padding),
            )
        }
    }
}

@Composable
private fun TryOnContent(
    state: TryOnUiState.Content,
    viewModel: TryOnViewModel,
    modifier: Modifier,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val shade = state.descriptor.variantById(state.selectedVariantId)

    Box(modifier = modifier.fillMaxSize()) {
        FaceArTryOnSurface(
            state = TryOnSurfaceState(
                descriptor = state.descriptor,
                effect = state.effect,
                unavailableReason = state.unavailable,
                productTitle = state.productTitle,
                priceLabel = state.priceLabel,
                selectedVariantId = state.selectedVariantId,
                look = state.look,
                bagEnabled = state.bag is TryOnBagTarget.Purchasable,
                bagBusy = state.bagBusy,
                diagnostics = state.diagnostics,
            ),
            actions = TryOnSurfaceActions(
                onSelectVariant = viewModel::selectVariant,
                onLookChange = viewModel::setLook,
                onAddToBag = viewModel::addToBag,
                onCaptured = { bitmap ->
                    // Stamping and encoding a full-resolution bitmap on the
                    // main thread is a visible stutter on the frame right
                    // after the shutter.
                    scope.launch {
                        val caption = captureCaption(state.productTitle, shade?.label)
                        val file = withContext(Dispatchers.IO) {
                            writeCapture(context, stampCapture(bitmap, caption))
                        }
                        if (file == null) {
                            viewModel.report(CAPTURE_FAILED)
                        } else {
                            // Straight to the chooser. The control says
                            // "share this look" and this is it doing that —
                            // no intermediate preview asking what they meant.
                            shareCapture(context, file, subject = caption)
                        }
                    }
                },
                onProblem = viewModel::report,
            ),
            modifier = Modifier.fillMaxSize(),
        )

        state.message?.let { message ->
            CommerceNotice(
                text = message,
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .padding(UsTheme.spacing.pageHorizontal),
            )
            // These lines are outcomes ("Added to your bag"), not states. Left
            // on screen they would still be claiming something that happened a
            // minute ago, over a live camera.
            LaunchedEffect(message) {
                delay(NOTICE_MS)
                viewModel.dismissMessage()
            }
        }
    }
}

private const val NOTICE_MS = 3_000L
private const val CAPTURE_FAILED = "The photo could not be saved."
