package com.us.android.feature.dating.onboarding

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.OnboardingStep
import com.us.android.feature.dating.location.LocationEffect
import com.us.android.feature.dating.location.LocationStep
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.UpsertProfileRequest
import com.us.android.feature.dating.ui.BottomAction
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.Tone
import java.time.LocalDate
import java.time.Period

/** The values dating-service stores. `interested_in_gender` must EQUAL a candidate's `gender` to match. */
object DatingChoices {
    val intents = listOf(
        UsChoice("casual", "Something casual"),
        UsChoice("serious", "Something serious"),
        UsChoice("marriage", "Marriage"),
    )
    val genders = listOf(
        UsChoice("woman", "Woman"),
        UsChoice("man", "Man"),
        UsChoice("nonbinary", "Non-binary"),
    )
    val distances = listOf(
        UsChoice(5, "5 km"),
        UsChoice(10, "10 km"),
        UsChoice(25, "25 km"),
        UsChoice(50, "50 km"),
        UsChoice(100, "100 km"),
    )

    fun intentLabel(wire: String?): String = intents.firstOrNull { it.value == wire }?.label ?: ""
}

/** Whole years since an RFC 3339 birth date ("1995-04-12T00:00:00Z"); null when it cannot be read. */
fun ageFrom(birthDate: String?, today: LocalDate = LocalDate.now()): Int? {
    val date = birthDate?.take(ISO_DATE_LENGTH)?.let { runCatching { LocalDate.parse(it) }.getOrNull() } ?: return null
    return Period.between(date, today).years.takeIf { it >= 0 }
}

private const val ISO_DATE_LENGTH = 10

/**
 * One draft step, chosen by the gate. [onSaved] makes the root re-read the
 * profile: the server decides what comes next.
 */
@Composable
fun DraftStepScreen(
    step: OnboardingStep,
    profile: DatingProfileDto?,
    preferences: PreferencesDto?,
    identityIncomplete: Boolean,
    onBack: () -> Unit,
    onSaved: () -> Unit,
    viewModel: OnboardingViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.savedCount) { if (state.savedCount > 0) onSaved() }

    state.consentPrompt?.let { type ->
        ConfirmDialog(
            title = type.title,
            body = type.body,
            confirmLabel = "I agree",
            dismissLabel = "Not now",
            onConfirm = { viewModel.onConsentAnswered(granted = true) },
            onDismiss = { viewModel.onConsentAnswered(granted = false) },
        )
    }

    val title = when (step) {
        OnboardingStep.CREATE -> "Dating"
        OnboardingStep.BASICS -> "About you"
        OnboardingStep.LOCATION -> "Where you are"
        else -> "Who you'd like to meet"
    }
    DatingScreen(title = title, onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(top = padding.calculateTopPadding(), bottom = padding.calculateBottomPadding())
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            when (step) {
                OnboardingStep.CREATE -> CreateStep(saving = state.saving, onCreate = viewModel::create)
                OnboardingStep.BASICS -> BasicsStep(profile, identityIncomplete, state.saving, viewModel::saveBasics)
                OnboardingStep.LOCATION -> LocationStepContent(state, viewModel)
                else -> PreferencesStep(profile, preferences, state.saving, viewModel::savePreferences)
            }
        }
    }
}

@Composable
private fun CreateStep(saving: Boolean, onCreate: (String) -> Unit) {
    var intent by rememberSaveable { mutableStateOf<String?>(null) }
    Text("Meet people nearby, safely.", style = MaterialTheme.typography.headlineSmall, color = UsTheme.extended.textPrimary)
    Text(
        "Your first name and age come from your Momentum account. Photos stay blurred until you match, " +
            "and others only ever see a distance range, never where you are.",
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textMuted,
    )
    UsChoiceRow(options = DatingChoices.intents, selected = intent, onSelect = { intent = it }, label = "What are you looking for?")
    UsButton(
        text = "Create my dating profile",
        onClick = { intent?.let(onCreate) },
        enabled = intent != null,
        loading = saving,
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
private fun BasicsStep(
    profile: DatingProfileDto?,
    identityIncomplete: Boolean,
    saving: Boolean,
    onSave: (UpsertProfileRequest) -> Unit,
) {
    var intent by rememberSaveable { mutableStateOf(profile?.intent?.takeIf { it.isNotBlank() }) }
    var gender by rememberSaveable { mutableStateOf(profile?.gender) }
    var bio by rememberSaveable { mutableStateOf(profile?.bio.orEmpty()) }
    var height by rememberSaveable { mutableStateOf(profile?.heightCm?.toString().orEmpty()) }
    var religion by rememberSaveable { mutableStateOf(profile?.religion.orEmpty()) }
    var community by rememberSaveable { mutableStateOf(profile?.community.orEmpty()) }

    DatingCard {
        SectionLabel("From your Momentum account")
        val age = ageFrom(profile?.birthDate)
        Text(
            text = listOfNotNull(profile?.firstName?.takeIf { it.isNotBlank() }, age?.toString()).joinToString(", ").ifBlank { "—" },
            style = MaterialTheme.typography.titleLarge,
            color = UsTheme.extended.textPrimary,
        )
        InfoNote("Your name and birth date can't be changed here.")
        if (identityIncomplete) {
            InfoNote(
                "Your Momentum account is missing a first name or birth date. Add them in your account settings to continue.",
                tone = Tone.Warning,
            )
        }
    }
    UsChoiceRow(options = DatingChoices.intents, selected = intent, onSelect = { intent = it }, label = "Looking for")
    UsChoiceRow(options = DatingChoices.genders, selected = gender, onSelect = { gender = it }, label = "I am a")
    UsTextField(value = bio, onValueChange = { bio = it.take(MAX_BIO) }, label = "About me", singleLine = false)
    UsTextField(
        value = height,
        onValueChange = { v -> height = v.filter { it.isDigit() }.take(HEIGHT_DIGITS) },
        label = "Height in cm (optional)",
        keyboardType = KeyboardType.Number,
    )
    SectionLabel("Optional and sensitive")
    InfoNote("We'll ask for your consent before saving either of these.")
    UsTextField(value = religion, onValueChange = { religion = it.take(MAX_SHORT) }, label = "Religion (optional)")
    UsTextField(value = community, onValueChange = { community = it.take(MAX_SHORT) }, label = "Community (optional)")
    UsButton(
        text = "Continue",
        enabled = intent != null && gender != null,
        loading = saving,
        modifier = Modifier.fillMaxWidth(),
        onClick = {
            onSave(
                UpsertProfileRequest(
                    intent = intent,
                    gender = gender,
                    bio = bio.trim(),
                    heightCm = height.toIntOrNull(),
                    religion = religion.trim().takeIf { it.isNotEmpty() },
                    community = community.trim().takeIf { it.isNotEmpty() },
                ),
            )
        },
    )
}

@Composable
private fun LocationStepContent(state: OnboardingUiState, viewModel: OnboardingViewModel) {
    val context = LocalContext.current
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val granted = grants.values.any { it }
        val activity = context.findActivity()
        val canAskAgain = activity != null &&
            ActivityCompat.shouldShowRequestPermissionRationale(activity, Manifest.permission.ACCESS_COARSE_LOCATION)
        viewModel.onPermissionResult(granted, canAskAgain)
    }
    var city by rememberSaveable { mutableStateOf("") }

    Text("Find people near you", style = MaterialTheme.typography.headlineSmall, color = UsTheme.extended.textPrimary)
    Text(
        "We keep a point rounded to about 1 km. Other people only ever see a range like \"5–10 km\".",
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textMuted,
    )
    UsButton(
        text = "Use my location",
        loading = state.saving || state.location == LocationStep.Locating,
        modifier = Modifier.fillMaxWidth(),
        onClick = { viewModel.onUseMyLocation() },
    )
    when (val step = state.location) {
        is LocationStep.Denied -> InfoNote(
            if (step.canAskAgain) {
                "Location wasn't allowed. You can type your city instead."
            } else {
                "Location is turned off for Momentum. You can allow it in Settings, or type your city."
            },
            tone = Tone.Warning,
        )
        LocationStep.Unavailable -> InfoNote("We couldn't get your location. Type your city instead.", tone = Tone.Warning)
        else -> Unit
    }
    SectionLabel("Or type your city")
    UsTextField(value = city, onValueChange = { city = it.take(MAX_SHORT) }, label = "City")
    UsSecondaryButton(text = "Save city", enabled = city.isNotBlank() && !state.saving, onClick = { viewModel.saveCity(city) }, modifier = Modifier.fillMaxWidth())

    if (state.location == LocationStep.ExplainingPermission) {
        ConfirmDialog(
            title = "Allow location?",
            body = "Dating uses your approximate location to show people nearby and to let you share it with someone " +
                "you trust. It's rounded to about 1 km, and nobody sees where you are.",
            confirmLabel = "Continue",
            dismissLabel = "Not now",
            onConfirm = {
                if (viewModel.onRationaleAccepted() == LocationEffect.RequestPermission) {
                    launcher.launch(arrayOf(Manifest.permission.ACCESS_COARSE_LOCATION, Manifest.permission.ACCESS_FINE_LOCATION))
                }
            },
            onDismiss = viewModel::onRationaleDismissed,
        )
    }
}

@Composable
private fun PreferencesStep(
    profile: DatingProfileDto?,
    preferences: PreferencesDto?,
    saving: Boolean,
    onSave: (interestedIn: String, minAge: Int, maxAge: Int, distanceKm: Int) -> Unit,
) {
    var interestedIn by rememberSaveable { mutableStateOf(preferences?.interestedInGender) }
    var minAge by rememberSaveable { mutableStateOf((preferences?.minAge ?: DEFAULT_MIN_AGE).toString()) }
    var maxAge by rememberSaveable { mutableStateOf((preferences?.maxAge ?: DEFAULT_MAX_AGE).toString()) }
    var distance by rememberSaveable { mutableStateOf(preferences?.distanceKm?.takeIf { it > 0 } ?: DEFAULT_DISTANCE) }
    val min = minAge.toIntOrNull()
    val max = maxAge.toIntOrNull()
    val agesValid = min != null && max != null && min >= OnboardingViewModel.MIN_AGE && max <= OnboardingViewModel.MAX_AGE && min <= max

    if (profile?.gender == null) InfoNote("Tell us about yourself first.")
    UsChoiceRow(options = DatingChoices.genders, selected = interestedIn, onSelect = { interestedIn = it }, label = "Show me")
    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        UsTextField(
            value = minAge,
            onValueChange = { v -> minAge = v.filter { it.isDigit() }.take(AGE_DIGITS) },
            label = "From age",
            keyboardType = KeyboardType.Number,
            modifier = Modifier.weight(1f),
        )
        UsTextField(
            value = maxAge,
            onValueChange = { v -> maxAge = v.filter { it.isDigit() }.take(AGE_DIGITS) },
            label = "To age",
            keyboardType = KeyboardType.Number,
            modifier = Modifier.weight(1f),
            errorText = if (min != null && max != null && !agesValid) "18 or older, and 'from' no more than 'to'" else null,
        )
    }
    UsChoiceRow(
        options = DatingChoices.distances,
        selected = distance,
        onSelect = { it?.let { d -> distance = d } },
        label = "Up to",
        allowDeselect = false,
    )
    UsButton(
        text = "Continue",
        enabled = interestedIn != null && agesValid,
        loading = saving,
        modifier = Modifier.fillMaxWidth(),
        onClick = { if (min != null && max != null) interestedIn?.let { onSave(it, min, max, distance) } },
    )
}

/** pending_photo, and photo editing later. [onContinue] is offered once a primary photo is approved. */
@Composable
fun PhotosScreen(
    onBack: () -> Unit,
    onContinue: (() -> Unit)?,
    onOpenPrompts: () -> Unit,
    viewModel: PhotosViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        if (uri != null) viewModel.addPhoto(uri.toString())
    }
    var selected by remember { mutableStateOf<OwnPhoto?>(null) }

    DatingScreen(
        title = "Your photos",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (onContinue != null) {
                BottomAction(
                    label = if (state.hasApprovedPrimary) "Continue" else "Waiting for an approved main photo",
                    enabled = state.hasApprovedPrimary,
                    onClick = onContinue,
                )
            }
        },
    ) { padding ->
        if (state.loading) {
            LoadingPane()
            return@DatingScreen
        }
        LazyColumn(
            contentPadding = com.us.android.feature.dating.ui.listPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item {
                InfoNote("Your main photo must clearly show your face. People who haven't matched with you see it blurred.")
            }
            state.uploadProgress?.let { progress ->
                item { LinearProgressIndicator(progress = { progress }, modifier = Modifier.fillMaxWidth(), color = UsTheme.extended.accentSolid) }
            }
            items(state.photos.chunked(PHOTOS_PER_ROW)) { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    row.forEach { photo ->
                        Box(
                            modifier = Modifier
                                .weight(1f)
                                .aspectRatio(PHOTO_RATIO)
                                .clip(RoundedCornerShape(UsTheme.radii.medium))
                                .clickable { selected = photo },
                        ) {
                            DatingPhoto(url = photo.url, contentDescription = "Your photo", modifier = Modifier.fillMaxSize())
                            Column(Modifier.align(Alignment.BottomStart).padding(6.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                if (photo.primary) Pill("Main", Tone.Accent)
                                Pill(
                                    photo.status.label,
                                    when (photo.status) {
                                        PhotoStatus.APPROVED -> Tone.Positive
                                        PhotoStatus.CHECKING -> Tone.Warning
                                        PhotoStatus.REJECTED -> Tone.Danger
                                    },
                                )
                            }
                        }
                    }
                    repeat(PHOTOS_PER_ROW - row.size) { Box(Modifier.weight(1f)) }
                }
            }
            item {
                UsSecondaryButton(
                    text = "Add a photo",
                    enabled = state.canAddMore,
                    modifier = Modifier.fillMaxWidth(),
                    onClick = { picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                )
            }
            item {
                UsSecondaryButton(text = "Answer a few prompts (optional)", onClick = onOpenPrompts, modifier = Modifier.fillMaxWidth())
            }
        }
    }

    selected?.let { photo ->
        ConfirmDialog(
            title = "This photo",
            body = if (photo.primary) "This is your main photo." else "Make this your main photo, or remove it.",
            confirmLabel = if (photo.primary) "Remove" else "Make main",
            destructive = photo.primary,
            dismissLabel = if (photo.primary) "Close" else "Remove",
            onConfirm = {
                if (photo.primary) viewModel.delete(photo.id) else viewModel.makePrimary(photo.id)
                selected = null
            },
            onDismiss = {
                if (!photo.primary) viewModel.delete(photo.id)
                selected = null
            },
        )
    }
}

@Composable
fun PromptsScreen(onBack: () -> Unit, viewModel: PromptsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    DatingScreen(title = "Prompts", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        if (state.loading) {
            LoadingPane()
            return@DatingScreen
        }
        LazyColumn(contentPadding = com.us.android.feature.dating.ui.listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            items(state.catalog, key = { it.id }) { prompt ->
                var text by rememberSaveable(prompt.id, state.answers[prompt.id]) { mutableStateOf(state.answers[prompt.id].orEmpty()) }
                DatingCard {
                    Text(prompt.question, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                    UsTextField(
                        value = text,
                        onValueChange = { text = it },
                        label = "Your answer",
                        singleLine = false,
                        errorText = if (PromptsViewModel.fits(text)) null else "Too long",
                    )
                    UsSecondaryButton(
                        text = if (text.isBlank() && state.answers.containsKey(prompt.id)) "Remove" else "Save",
                        enabled = !state.saving && PromptsViewModel.fits(text) && text.trim() != state.answers[prompt.id].orEmpty(),
                        onClick = { viewModel.answer(prompt.id, text) },
                    )
                }
            }
        }
    }
}

/** A step the person cannot act on from here: under review, paused, or held. */
@Composable
fun StatusPane(step: OnboardingStep, onBack: () -> Unit, onUnpause: () -> Unit, onOpenPrivacy: () -> Unit) {
    DatingScreen(title = "Dating", onBack = onBack) { _ ->
        when (step) {
            OnboardingStep.REVIEW -> com.us.android.feature.dating.ui.MessagePane(
                title = "We're reviewing your profile",
                body = "A moderator is checking your profile. This usually doesn't take long, and we'll let you know.",
                icon = UsIcons.Clock,
            )
            OnboardingStep.PAUSED -> com.us.android.feature.dating.ui.MessagePane(
                title = "Your profile is paused",
                body = "Nobody new sees you while you're paused. Your matches and chats stay.",
                icon = UsIcons.Pause,
                primaryLabel = "Resume dating",
                onPrimary = onUnpause,
                secondaryLabel = "Privacy and data",
                onSecondary = onOpenPrivacy,
            )
            else -> com.us.android.feature.dating.ui.MessagePane(
                title = "Your dating profile is on hold",
                body = "Your profile isn't visible right now while our team looks into it. You can still manage your data.",
                icon = UsIcons.Lock,
                secondaryLabel = "Privacy and data",
                onSecondary = onOpenPrivacy,
            )
        }
    }
}

private fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}

private const val MAX_BIO = 500
private const val MAX_SHORT = 60
private const val HEIGHT_DIGITS = 3
private const val AGE_DIGITS = 3
private const val DEFAULT_MIN_AGE = 18
private const val DEFAULT_MAX_AGE = 45
private const val DEFAULT_DISTANCE = 25
private const val PHOTOS_PER_ROW = 3
private const val PHOTO_RATIO = 0.8f
