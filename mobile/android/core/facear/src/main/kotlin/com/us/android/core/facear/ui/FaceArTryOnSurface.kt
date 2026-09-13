package com.us.android.core.facear.ui

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.util.Log
import android.view.SurfaceHolder
import android.view.SurfaceView
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.facear.BuildConfig
import com.us.android.core.facear.TRY_ON_LOG_TAG
import com.us.android.core.facear.TryOnCoverage
import com.us.android.core.facear.TryOnDescriptor
import com.us.android.core.facear.TryOnDiagnostics
import com.us.android.core.facear.TryOnEffect
import com.us.android.core.facear.TryOnFinish
import com.us.android.core.facear.TryOnJsContract
import com.us.android.core.facear.TryOnKind
import com.us.android.core.facear.TryOnLoadOutcome
import com.us.android.core.facear.TryOnLook
import com.us.android.core.facear.TryOnVariant
import com.us.android.core.facear.tryOnPreviewNotice
import com.us.android.core.facear.diagnosticLines
import com.us.android.core.facear.tryOnJsCall
import com.us.android.core.facear.tryOnProbe
import com.us.android.core.ui.UsEmptyState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Everything the try-on surface RENDERS.
 *
 * One object rather than a dozen parameters, because the surface is the whole
 * screen and a dozen positional arguments is how a caller eventually passes
 * the price where the title goes.
 */
data class TryOnSurfaceState(
    val descriptor: TryOnDescriptor,
    /** The bundle on disk, or null — which means no camera is opened at all. */
    val effect: TryOnEffect?,
    /** A licence or effect refusal, already worded for the viewer. */
    val unavailableReason: String?,
    val productTitle: String,
    /** Already formatted, with its symbol. `:core:facear` knows nothing about money. */
    val priceLabel: String?,
    val selectedVariantId: String?,
    val look: TryOnLook = TryOnLook.DEFAULT,
    /** False when this shade maps to nothing purchasable, or stock is out. */
    val bagEnabled: Boolean = false,
    val bagBusy: Boolean = false,
    /** The parts of the diagnostic picture the HOST knows: licence, resolution. */
    val diagnostics: TryOnDiagnostics = TryOnDiagnostics(),
)

/** Everything the try-on surface can ask its host to do. */
data class TryOnSurfaceActions(
    val onSelectVariant: (TryOnVariant) -> Unit,
    val onLookChange: (TryOnLook) -> Unit,
    val onAddToBag: () -> Unit,
    val onCaptured: (Bitmap) -> Unit,
    val onProblem: (String) -> Unit,
)

/**
 * The live try-on: a camera with the product on your face, the shades you can
 * buy, and a way to buy them.
 *
 * ## WHAT THIS IS FOR, WHICH IS NOT "A CAMERA"
 *
 * A try-on is worth building for exactly one reason: it answers "does this
 * shade suit me" faster than a swatch on a white background can. Everything
 * here serves that and nothing else —
 *
 *  * the product and its price stay legible over the preview, because the
 *    shopper must never have to go back to remember what they are looking at;
 *  * the shade carousel is the real hex of each purchasable variant, with the
 *    chosen one ringed and NAMED, because "Crimson" is how a lipstick is
 *    remembered and a circle is not;
 *  * **hold to compare** takes the colour off while held. This is the single
 *    most convincing control a try-on has, and it costs no new effect
 *    vocabulary: the contract's own "no colour" payload already means bare
 *    lips (see `tryOnJsCall(bare = true)`);
 *  * finish and coverage are offered because the prefab already implements
 *    both and a shopper who can choose sheer over full has been sold
 *    something a swatch cannot sell;
 *  * **Add to bag** is on this screen. A try-on that cannot buy is a toy.
 *
 * ## WHAT THIS DRAWS WHEN THERE IS NO EFFECT
 *
 * A sentence. Not a camera. Most of the catalogue has no bundle, and a face
 * with nothing on it and no explanation reads as a broken feature.
 *
 * ## THE ORDER THE EFFECT IS LOADED IN
 *
 * After the surface exists — never on composition. See [FaceArSession].
 *
 * ## THE PERMISSION
 *
 * The app DECLARES `android.permission.CAMERA` (`:core:call` needs it for
 * video calls), and once a manifest declares it Android insists the runtime
 * grant exists. This screen asks once on entry and, if refused, shows a line
 * and a button rather than a dead preview. No blind retry loop.
 */
@Composable
fun FaceArTryOnSurface(
    state: TryOnSurfaceState,
    actions: TryOnSurfaceActions,
    modifier: Modifier = Modifier,
) {
    // Two different "no" answers, one surface: a licence refusal the gate
    // already worded, or an effect bundle that is not on the device. Either
    // way NO camera is opened.
    val effect = state.effect
    if (state.unavailableReason != null || effect == null) {
        UsEmptyState(
            title = "Try-on is not ready",
            detail = state.unavailableReason ?: NO_EFFECT,
            modifier = modifier.fillMaxSize(),
        )
        return
    }

    val context = LocalContext.current
    var granted by rememberSaveable { mutableStateOf(context.holdsCamera()) }
    val request = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { allowed -> granted = allowed }
    // Asked once, on entry. A full-screen camera destination the viewer chose
    // to open is the one place a prompt needs no preamble; a refusal is
    // answered below rather than re-asked.
    LaunchedEffect(Unit) { if (!granted) request.launch(Manifest.permission.CAMERA) }

    if (!granted) {
        CameraDenied(onAsk = { request.launch(Manifest.permission.CAMERA) }, modifier = modifier)
        return
    }

    FaceArCamera(state = state, effect = effect, actions = actions, modifier = modifier)
}

@Composable
@Suppress("LongMethod")
private fun FaceArCamera(
    state: TryOnSurfaceState,
    effect: TryOnEffect,
    actions: TryOnSurfaceActions,
    modifier: Modifier,
) {
    val context = LocalContext.current
    val latestCaptured = rememberUpdatedState(actions.onCaptured)
    val latestProblem = rememberUpdatedState(actions.onProblem)
    var surfaceWidth by remember { mutableStateOf(0) }
    var surfaceHeight by remember { mutableStateOf(0) }
    var front by remember { mutableStateOf(true) }
    var comparing by remember { mutableStateOf(false) }
    var diagnosticsOpen by rememberSaveable { mutableStateOf(false) }

    val session = remember(context) {
        FaceArSession(
            context = context,
            onPhoto = { bitmap -> latestCaptured.value(bitmap) },
            onCameraFailed = { message -> latestProblem.value(message) },
        )
    }
    val report by session.report.collectAsState()

    // recycle() exactly once, when the destination leaves. The licence is NOT
    // deinitialised — see FaceArSession.release.
    DisposableEffect(session) { onDispose { session.release() } }

    // Camera and effect follow the host's lifecycle, not just composition: a
    // screen that is merely stopped (notification shade, incoming call) must
    // give the camera back.
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner, session) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START, Lifecycle.Event.ON_RESUME -> session.resume()
                Lifecycle.Event.ON_PAUSE, Lifecycle.Event.ON_STOP -> session.pause()
                else -> Unit
            }
        }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }

    val probe = TryOnJsContract.methodFor(state.descriptor.kind)?.let(::tryOnProbe)

    // ONLY once a surface exists, and only for a bundle that can draw.
    //
    // The surface first, because an effect created before the render thread has
    // an EGL surface is the SDK's own "No surface, failed to create effect" —
    // which presents as a camera that never draws anything.
    //
    // And a bundle whose manifest declares NO scene content is never loaded at
    // all: the player would render that empty scene instead of the camera, and
    // a black rectangle is a worse answer than a plain camera with one honest
    // line. See tryOnPreviewNotice.
    LaunchedEffect(session, report.surfaceReady, effect.loadPath, effect.declaresSceneContent) {
        if (!report.surfaceReady || effect.declaresSceneContent == false) return@LaunchedEffect
        // Off the main thread: loadEffect(path, true) blocks on the render
        // thread, which on a first shader compile is long enough to be an ANR,
        // and the readiness probe blocks on the interpreter.
        withContext(Dispatchers.IO) { session.loadEffect(effect.loadPath, probe) }
    }

    // The variant is applied as a JS call built by a PURE function — never
    // assembled here — and sent through every entry point that could work.
    // Keyed on the load outcome as well as the selection, because a shade
    // chosen before the effect existed has to be re-sent once it does.
    LaunchedEffect(session, report.load, report.unloadedAsBlank, state.selectedVariantId, state.look, comparing) {
        // No effect in the player means no shade to send — which is the normal
        // state for a bundle that was refused for being blank, and the shade
        // carousel keeps working regardless because the SELECTION is ours.
        if (report.load !is TryOnLoadOutcome.Loaded || report.unloadedAsBlank) return@LaunchedEffect
        val variant = state.descriptor.variantById(state.selectedVariantId)
        val call = tryOnJsCall(state.descriptor, variant, state.look, bare = comparing)
            ?: return@LaunchedEffect
        withContext(Dispatchers.IO) { session.apply(call, probe) }
    }

    val diagnostics = state.diagnostics.copy(
        effect = effect,
        surfaceReady = report.surfaceReady,
        load = report.load,
        drawable = report.drawable,
        effectUnloaded = report.unloadedAsBlank,
        sdkError = report.sdkError,
        sdkHint = report.sdkHint,
        activated = report.activated,
        apply = report.apply,
        variantLabel = state.descriptor.variantById(state.selectedVariantId)?.label,
        variantHex = state.descriptor.variantById(state.selectedVariantId)?.hex,
        look = state.look,
    )
    // One line, in the ORDINARY UI, when the camera works but nothing can be
    // drawn on the face. Not the vendor's words — those go in the debug panel.
    val previewNotice = tryOnPreviewNotice(effect, report.drawable)
    // The same lines the panel shows, under one stable tag, so a future
    // session that DOES have adb reads them without opening the panel.
    LaunchedEffect(diagnostics) {
        diagnosticLines(diagnostics).forEach { Log.i(TRY_ON_LOG_TAG, it) }
    }

    Box(modifier = modifier.fillMaxSize().background(UsTheme.extended.bgCanvas)) {
        AndroidView(
            modifier = Modifier
                .fillMaxSize()
                .onSizeChanged { size ->
                    surfaceWidth = size.width
                    surfaceHeight = size.height
                },
            factory = { viewContext ->
                SurfaceView(viewContext).apply {
                    holder.addCallback(
                        object : SurfaceHolder.Callback {
                            override fun surfaceCreated(holder: SurfaceHolder) {
                                session.attach(holder.surface)
                            }

                            override fun surfaceChanged(
                                holder: SurfaceHolder,
                                format: Int,
                                width: Int,
                                height: Int,
                            ) = session.surfaceChanged(format, width, height)

                            override fun surfaceDestroyed(holder: SurfaceHolder) =
                                session.surfaceDestroyed()
                        },
                    )
                }
            },
        )

        ProductHeader(
            title = state.productTitle,
            price = state.priceLabel,
            comparing = comparing,
            modifier = Modifier.align(Alignment.TopStart).fillMaxWidth(),
        )

        Column(
            modifier = Modifier
                .align(Alignment.TopEnd)
                .padding(UsTheme.spacing.pageHorizontal),
            horizontalAlignment = Alignment.End,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            GlyphButton(
                icon = UsIcons.RotateCw,
                description = if (front) "Switch to the back camera" else "Switch to the front camera",
            ) {
                session.flip()
                front = !front
            }
            // DEBUG ONLY. It prints the vendor's raw errors and the effect's
            // internals; nothing here ships in front of a shopper.
            if (BuildConfig.DEBUG) {
                TryOnDiagnosticsPanel(
                    diagnostics = diagnostics,
                    expanded = diagnosticsOpen,
                    onToggle = { diagnosticsOpen = !diagnosticsOpen },
                )
            }
        }

        TryOnControls(
            state = state,
            actions = actions,
            previewNotice = previewNotice,
            comparing = comparing,
            onComparingChange = { comparing = it },
            onCapture = { session.capture(surfaceWidth, surfaceHeight) },
            captureEnabled = surfaceWidth > 0 && surfaceHeight > 0,
            modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth(),
        )
    }
}

/**
 * The product, over the preview.
 *
 * A scrim and not a bar: a bar would eat the top of the shopper's face, which
 * is the one thing this screen exists to show.
 */
@Composable
private fun ProductHeader(
    title: String,
    price: String?,
    comparing: Boolean,
    modifier: Modifier,
) {
    Column(
        modifier = modifier
            .background(Brush.verticalGradient(listOf(SCRIM, Color.Transparent)))
            .padding(
                start = UsTheme.spacing.pageHorizontal,
                end = HEADER_END_GUTTER,
                top = UsTheme.spacing.xxl,
                bottom = UsTheme.spacing.xxxxl,
            ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge,
            color = Color.White,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Row(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            price?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.titleMedium,
                    color = UsTheme.extended.accentSolid,
                )
            }
            if (comparing) {
                Text(
                    text = "Showing your bare face",
                    style = MaterialTheme.typography.labelMedium,
                    color = Color.White,
                )
            }
        }
    }
}

@Composable
@Suppress("LongParameterList")
private fun TryOnControls(
    state: TryOnSurfaceState,
    actions: TryOnSurfaceActions,
    previewNotice: String?,
    comparing: Boolean,
    onComparingChange: (Boolean) -> Unit,
    onCapture: () -> Unit,
    captureEnabled: Boolean,
    modifier: Modifier,
) {
    val selected = state.descriptor.variantById(state.selectedVariantId)
    val previewLive = previewNotice == null
    Column(
        modifier = modifier
            .background(Brush.verticalGradient(listOf(Color.Transparent, SCRIM)))
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.xxl,
            ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        // One line, and not the vendor's. The camera, the shades and the bag
        // all keep working, so this says that rather than pretending nothing is
        // wrong or throwing a full error state over a live camera.
        previewNotice?.let { notice ->
            Text(
                text = notice,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.accentSolid,
            )
        }

        // Finish and coverage only for makeup: they are makeup_lipsshine's own
        // settings, and offering them for a kind whose bundle does not
        // implement them would be a control that does nothing. Hidden entirely
        // when there is no live preview, for the same reason — they change how
        // a shade is DRAWN, so with nothing drawing they are noise.
        if (state.descriptor.kind == TryOnKind.MAKEUP && previewLive) {
            LookRow(
                labels = TryOnFinish.entries.map { it.label },
                selectedIndex = TryOnFinish.entries.indexOf(state.look.finish),
                onSelect = { index ->
                    actions.onLookChange(state.look.copy(finish = TryOnFinish.entries[index]))
                },
            )
            LookRow(
                labels = TryOnCoverage.entries.map { it.label },
                selectedIndex = TryOnCoverage.entries.indexOf(state.look.coverage),
                onSelect = { index ->
                    actions.onLookChange(state.look.copy(coverage = TryOnCoverage.entries[index]))
                },
            )
        }

        if (state.descriptor.variants.isEmpty()) {
            // Honest, one line: the effect is on, but the seller listed no
            // shade to choose. The default the bundle carries is what is on
            // the face, and saying so beats an empty strip.
            Text(
                text = "This product lists no shades — you are seeing the default.",
                style = MaterialTheme.typography.bodySmall,
                color = Color.White,
            )
        } else {
            Text(
                text = selected?.label ?: "Choose a shade",
                style = MaterialTheme.typography.titleMedium,
                color = Color.White,
            )
            VariantStrip(
                variants = state.descriptor.variants,
                selectedId = state.selectedVariantId,
                onSelect = actions.onSelectVariant,
            )
        }

        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CompareButton(
                // A variant carrying a seller-written script has no "same look
                // without the colour", so the control is not offered rather
                // than offered and silently inert — and neither is it offered
                // when nothing is being drawn on the face to compare against.
                enabled = previewLive && selected != null && selected.js == null,
                comparing = comparing,
                onComparingChange = onComparingChange,
            )
            GlyphButton(
                icon = UsIcons.Share,
                description = "Share this look",
                enabled = captureEnabled,
                onClick = onCapture,
            )
        }

        UsButton(
            text = if (state.bagEnabled) "Add to bag" else "Not available in this shade",
            onClick = actions.onAddToBag,
            enabled = state.bagEnabled && !state.bagBusy,
            loading = state.bagBusy,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/**
 * Hold to see your bare face.
 *
 * Press-and-hold rather than a toggle, and that is the whole design: a toggle
 * leaves the shopper unsure which state they are looking at, while a held
 * button is unambiguous for exactly as long as it is held. `tryAwaitRelease`
 * also returns on a cancelled gesture, so a finger dragged off the control
 * cannot leave the face bare.
 */
@Composable
private fun CompareButton(
    enabled: Boolean,
    comparing: Boolean,
    onComparingChange: (Boolean) -> Unit,
) {
    val latest = rememberUpdatedState(onComparingChange)
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(if (comparing) UsTheme.extended.bgCardSolid else GLASS)
            .border(
                width = HAIRLINE,
                color = if (comparing) UsTheme.extended.accentSolid else UsTheme.extended.borderMedium,
                shape = RoundedCornerShape(UsTheme.radii.full),
            )
            .then(
                if (!enabled) {
                    Modifier
                } else {
                    Modifier.pointerInput(Unit) {
                        detectTapGestures(
                            onPress = {
                                latest.value(true)
                                tryAwaitRelease()
                                latest.value(false)
                            },
                        )
                    }
                },
            )
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
            .semantics { contentDescription = "Hold to see your face without the shade" },
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = UsIcons.Smile,
            contentDescription = null,
            tint = if (enabled) Color.White else UsTheme.extended.textMuted,
            modifier = Modifier.size(GLYPH),
        )
        Text(
            text = "Hold to compare",
            style = MaterialTheme.typography.labelLarge,
            color = if (enabled) Color.White else UsTheme.extended.textMuted,
        )
    }
}

/** One row of mutually exclusive words — the finish, or the coverage. */
@Composable
private fun LookRow(labels: List<String>, selectedIndex: Int, onSelect: (Int) -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        labels.forEachIndexed { index, label ->
            val chosen = index == selectedIndex
            Text(
                text = label,
                style = MaterialTheme.typography.labelMedium,
                color = if (chosen) Color.White else UsTheme.extended.textSecondary,
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(if (chosen) UsTheme.extended.bgCardSolid else GLASS)
                    .border(
                        width = HAIRLINE,
                        color = if (chosen) UsTheme.extended.accentSolid else Color.Transparent,
                        shape = RoundedCornerShape(UsTheme.radii.full),
                    )
                    .semantics {
                        contentDescription = label
                        role = Role.RadioButton
                    }
                    .clickable { onSelect(index) }
                    .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
            )
        }
    }
}

/** The shade carousel: a swatch per variant in its real hex, the chosen one ringed. */
@Composable
private fun VariantStrip(
    variants: List<TryOnVariant>,
    selectedId: String?,
    onSelect: (TryOnVariant) -> Unit,
) {
    LazyRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        items(variants, key = { it.id }) { variant ->
            val chosen = variant.id == selectedId
            Box(
                modifier = Modifier.size(SWATCH_SLOT),
                contentAlignment = Alignment.Center,
            ) {
                Box(
                    modifier = Modifier
                        .size(if (chosen) SWATCH_SELECTED else SWATCH)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        // White for the selection ring, not the ember accent:
                        // the accent is the primary action (Add to bag, right
                        // below) and two ember rings on one bar compete.
                        .border(
                            width = if (chosen) SELECTED_RING else UNSELECTED_RING,
                            color = if (chosen) Color.White else UsTheme.extended.borderMedium,
                            shape = RoundedCornerShape(UsTheme.radii.full),
                        )
                        .background(variant.swatch())
                        .semantics {
                            contentDescription = variant.label
                            role = Role.RadioButton
                        }
                        .clickable { onSelect(variant) },
                )
            }
        }
    }
}

@Composable
private fun CameraDenied(onAsk: () -> Unit, modifier: Modifier) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = "Try-on needs the camera",
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = "The preview is drawn on your own camera feed. Nothing is uploaded.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.padding(top = UsTheme.spacing.m),
        )
        UsButton(
            text = "Allow camera",
            onClick = onAsk,
            modifier = Modifier
                .padding(top = UsTheme.spacing.xxl)
                .fillMaxWidth(),
        )
    }
}

@Composable
private fun GlyphButton(
    icon: ImageVector,
    description: String,
    enabled: Boolean = true,
    onClick: () -> Unit,
) {
    Box(
        modifier = Modifier
            .size(GLYPH_BUTTON)
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(GLASS)
            .semantics { contentDescription = description }
            .clickable(enabled = enabled, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = if (enabled) Color.White else UsTheme.extended.textMuted,
            modifier = Modifier.size(GLYPH_LARGE),
        )
    }
}

/** A variant's swatch colour, or the card colour when it has none (a size, say). */
@Composable
private fun TryOnVariant.swatch(): Color =
    hex?.let { raw -> parseSwatch(raw) } ?: UsTheme.extended.bgCard

/**
 * The swatch colour for a hex, or null when the wire value is not one.
 *
 * Deliberately separate from the JS-call builder's `rgbOf`: that one produces
 * the effect's contract arguments and must be strict about the exact form, this
 * one only tints a circle and may fail to a neutral.
 */
private fun parseSwatch(hex: String): Color? {
    val digits = hex.trim().removePrefix("#")
    if (digits.length != HEX_DIGITS) return null
    return digits.toLongOrNull(HEX_RADIX)?.let { Color(it or OPAQUE) }
}

private fun Context.holdsCamera(): Boolean =
    ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) ==
        PackageManager.PERMISSION_GRANTED

private const val NO_EFFECT = "This build carries no try-on effect for this product yet."
private const val HEX_DIGITS = 6
private const val HEX_RADIX = 16
private const val OPAQUE = 0xFF000000L
private val SCRIM = Color(0xB3041122)
private val GLASS = Color(0x66041122)
private val SWATCH = 40.dp
private val SWATCH_SELECTED = 48.dp
private val SWATCH_SLOT = 52.dp
private val GLYPH_BUTTON = 44.dp
private val GLYPH = 16.dp
private val GLYPH_LARGE = 20.dp
private val SELECTED_RING = 3.dp
private val UNSELECTED_RING = 1.dp
private val HAIRLINE = 1.dp
private val HEADER_END_GUTTER = 72.dp
