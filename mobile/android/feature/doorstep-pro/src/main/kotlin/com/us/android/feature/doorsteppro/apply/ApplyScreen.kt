package com.us.android.feature.doorsteppro.apply

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.camera.rememberPhotoSource
import com.us.android.feature.doorsteppro.onboarding.*
import com.us.android.feature.doorsteppro.ui.*

private val titles = listOf("Personal details", "Services & location", "Identity & documents", "Bank details", "Review application")

@Composable
@OptIn(ExperimentalLayoutApi::class)
fun ApplyScreen(onBack: () -> Unit, onApplied: () -> Unit, viewModel: ApplyViewModel = hiltViewModel()) {
    val s by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(s.done) { if (s.done) onApplied() }
    val identity = rememberPhotoSource(onPicked = { viewModel.photo("identity", it) })
    val selfie = rememberPhotoSource(onPicked = { viewModel.photo("selfie", it) })
    val experience = rememberPhotoSource(onPicked = { viewModel.photo("experience", it) })
    val bank = rememberPhotoSource(onPicked = { viewModel.photo("bank", it) })
    val scroll = rememberScrollState()
    LaunchedEffect(s.step) { scroll.scrollTo(0) }
    ProScreen(title = "Partner application", onBack = { if (!s.submitting) { if (s.step == 0) onBack() else viewModel.back() } }, message = s.message, onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(if (s.step == 4) "Submit for verification" else "Continue", viewModel::submit, loading = s.submitting, enabled = !s.loading) }) { padding ->
        Column(Modifier.fillMaxSize().verticalScroll(scroll).padding(listPadding(padding)), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            Text("Step ${s.step + 1} of 5", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textMuted)
            LinearProgressIndicator(progress = { (s.step + 1) / 5f }, modifier = Modifier.fillMaxWidth())
            ProCard {
                CardHeading(titles[s.step])
                when (s.step) {
                    0 -> {
                        UsTextField(s.first, { v -> viewModel.edit { it.copy(first = v.take(50)) } }, label = "First name *", errorText = s.errors["first"])
                        UsTextField(s.last, { v -> viewModel.edit { it.copy(last = v.take(50)) } }, label = "Last name *", errorText = s.errors["last"])
                        Text("Gender *", style = MaterialTheme.typography.labelLarge)
                        FlowRow { listOf("male" to "Male", "female" to "Female", "others" to "Other").forEach { (key, label) ->
                            Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) { RadioButton(s.gender == key, { viewModel.edit { it.copy(gender = key) } }); Text(label) }
                        } }
                        UsTextField(s.alternate, { v -> viewModel.edit { it.copy(alternate = v.filter(Char::isDigit).take(10)) } }, label = "Alternate number (optional)", keyboardType = KeyboardType.Phone, errorText = s.errors["alternate"])
                        InfoNote("Alternate contact will need verification later.")
                    }
                    1 -> {
                        UsTextField(s.search, { v -> viewModel.edit { it.copy(search = v) } }, label = "Search service types")
                        if (s.catalogue.isEmpty()) UsPillButton("Reload services", viewModel::loadOptions, filled = false)
                        s.catalogue.filter { it.name.contains(s.search, true) }.forEach { skill ->
                            Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) { Checkbox(skill.code in s.skills, { checked -> viewModel.edit { it.copy(skills = if (checked) (it.skills + skill.code).distinct().take(20) else it.skills - skill.code) } }); Text(skill.name) }
                        }
                        ChoiceField("State *", s.stateCode, s.states.map { it.code to it.name }) { v -> viewModel.edit { it.copy(stateCode = v, town = "") } }
                        UsTextField(s.town, { v -> viewModel.edit { it.copy(town = v.take(100)) } }, label = "Town / city *", errorText = s.errors["town"])
                    }
                    2 -> {
                        UsTextField(s.pan, { v -> viewModel.edit { it.copy(pan = v.uppercase().filter(Char::isLetterOrDigit).take(10)) } }, label = "PAN number *", isPassword = true, errorText = s.errors["pan"])
                        InfoNote("PAN number only. No card upload.")
                        ChoiceField("Identity proof *", s.identityKind, listOf("passport" to "Passport", "voter_id" to "Voter ID", "driving_license" to "Driving licence")) { v -> viewModel.edit { it.copy(identityKind = v, identity = UploadUi.Idle) } }
                        UploadField("Identity document *", s.identity, identity.pickFromGallery)
                        UploadField("Selfie *", s.selfie, selfie.takePhoto)
                        InfoNote("A clear photo of your face. Private and used for verification only.")
                        UploadField("Experience / course certificate (optional)", s.experience, experience.pickFromGallery)
                        InfoNote("Optional. A certificate helps us understand your experience and recommend suitable assignments.")
                        if (s.experience != UploadUi.Idle) UsPillButton("Remove certificate", viewModel::removeExperience, filled = false)
                    }
                    3 -> BankFields(s.bank, s.errors, { v -> viewModel.edit { it.copy(bank = v) } }, bank.pickFromGallery)
                    4 -> {
                        LabeledValue("Name", "${s.first} ${s.last}")
                        LabeledValue("Location", "${s.town}, ${s.states.find { it.code == s.stateCode }?.name.orEmpty()}")
                        LabeledValue("Services", s.catalogue.filter { it.code in s.skills }.joinToString { it.name })
                        LabeledValue("PAN", "••••••${s.pan.takeLast(4)}")
                        LabeledValue("Bank", "${s.bank.bankName} · ••••${s.bank.account.takeLast(4)}")
                        Row { Checkbox(s.consent, { v -> viewModel.edit { it.copy(consent = v) } }); Text("These details and documents belong to me. I consent to verification.", style = MaterialTheme.typography.bodyMedium) }
                    }
                }
                s.errors.filterKeys { it in setOf("gender", "skills", "state", "identity", "selfie", "experience", "consent") }.values.distinct().forEach {
                    Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
}
